package scanner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"

	"github.com/JoshuaMart/fingerprinter/internal/browser"
	"github.com/JoshuaMart/fingerprinter/internal/chain"
	"github.com/JoshuaMart/fingerprinter/internal/config"
	"github.com/JoshuaMart/fingerprinter/internal/detection/detectors"
	"github.com/JoshuaMart/fingerprinter/internal/detection/engine"
	yamldet "github.com/JoshuaMart/fingerprinter/internal/detection/yaml"
	"github.com/JoshuaMart/fingerprinter/internal/httpclient"
	"github.com/JoshuaMart/fingerprinter/internal/metadata"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

// Scanner orchestrates the full scan pipeline.
type Scanner struct {
	cfg         *config.Config
	httpClient  *http.Client
	engine      *engine.Engine
	browserPool *browser.Pool
	semaphore   chan struct{}
}

// New creates a Scanner, loads detections, and starts the browser pool.
func New(cfg *config.Config) (*Scanner, error) {
	s := &Scanner{
		cfg: cfg,
		httpClient: httpclient.New(httpclient.Config{
			Timeout:  cfg.Scanner.RequestTimeout,
			ProxyURL: cfg.Scanner.Proxy,
			Headers:  cfg.Scanner.Headers,
		}),
		engine:    engine.New(),
		semaphore: make(chan struct{}, cfg.Scanner.ConcurrentScans),
	}

	// Load YAML detections
	defs, err := yamldet.LoadDir(cfg.Detections.YAMLDir)
	if err != nil {
		return nil, fmt.Errorf("loading YAML detections: %w", err)
	}
	for _, def := range defs {
		s.engine.Register(yamldet.NewDetector(def))
	}
	slog.Info("loaded YAML detections", "count", len(defs))

	// Register complex detectors
	for _, det := range detectors.All() {
		s.engine.Register(det)
	}

	// Start browser pool (mandatory)
	pool, err := browser.NewPool(cfg.Browser.PoolSize, cfg.Browser.PageTimeout, cfg.Browser.ControlURL, cfg.Scanner.Proxy, cfg.Scanner.UserHeaders)
	if err != nil {
		return nil, fmt.Errorf("starting browser pool: %w", err)
	}
	s.browserPool = pool

	return s, nil
}

// Scan executes the full scan pipeline for a single URL.
func (s *Scanner) Scan(ctx context.Context, req models.ScanRequest) (*models.ScanResult, error) {
	// Acquire semaphore
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Apply timeout
	timeout := time.Duration(req.Options.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = s.cfg.Server.ReadTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Validate URL
	if err := chain.ValidateURL(req.URL); err != nil {
		return nil, err
	}

	// Browser navigation + full scan pipeline inside callback (page is alive).
	// Chrome supports multiple concurrent pages, so probe404 and path checks
	// can open separate pages while the main page is still available for JS eval.
	var result *models.ScanResult

	err := s.browserPool.Navigate(ctx, req.URL, func(navResult *browser.NavigateResult) error {
		responses := navResult.Chain
		if len(responses) == 0 {
			return fmt.Errorf("no responses received")
		}

		finalResp := &responses[len(responses)-1]
		baseURL := finalResp.URL

		// Extract rendered HTML for goquery document
		var doc *goquery.Document
		renderedHTML, htmlErr := navResult.Page.HTML()
		if htmlErr != nil {
			slog.Warn("failed to extract rendered HTML", "error", htmlErr)
		} else {
			rendered := []byte(renderedHTML)
			if len(rendered) > len(finalResp.Body) {
				finalResp.Body = rendered
				finalResp.ResponseSize = len(rendered)
			}
			var parseErr error
			doc, parseErr = goquery.NewDocumentFromReader(bytes.NewReader(rendered))
			if parseErr != nil {
				slog.Warn("DOM parsing failed, continuing without document", "error", parseErr)
			}
		}

		// Pre-evaluate JS expressions while page is alive
		jsResults := make(map[string]string)
		jsExpressions := s.engine.CollectJSExpressions()
		s.evalJS(navResult.Page, jsExpressions, jsResults)

		// 404 probe via browser (separate page) — also evaluate JS on the 404 page
		detResponses := make([]models.ChainedResponse, len(responses))
		copy(detResponses, responses)
		if req.Options == nil || !req.Options.Skip404 {
			if notFound, probeJS, probeErr := s.probe404(ctx, baseURL, jsExpressions); probeErr == nil {
				detResponses = append(detResponses, *notFound)
				for k, v := range probeJS {
					if _, exists := jsResults[k]; !exists {
						jsResults[k] = v
					}
				}
			}
		}

		// Detections (parallel, handled by engine)
		detCtx := &models.DetectionContext{
			Responses:   detResponses,
			Document:    doc,
			HTTPClient:  s.httpClient,
			BrowserPool: s.browserPool,
			BrowserPage: navResult.Page,
			JSResults:   jsResults,
			BaseURL:     baseURL,
		}

		technologies := s.engine.Run(detCtx)

		// Metadata (HTTP — robots.txt, sitemap, favicon)
		cookies := chain.ExtractCookies(responses)
		scanMeta := metadata.Fetch(s.httpClient, baseURL, doc)

		// Aggregate
		result = &models.ScanResult{
			URL:           req.URL,
			Chain:         responses,
			Technologies:  technologies,
			Cookies:       cookies,
			Metadata:      scanMeta,
			ExternalHosts: navResult.ExternalHosts,
			ScannedAt:     time.Now().UTC(),
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	return result, nil
}

// evalJS evaluates a list of JS expressions on the given page and stores
// non-nil results in the results map.
func (s *Scanner) evalJS(page *rod.Page, expressions []string, results map[string]string) {
	for _, expr := range expressions {
		result, err := page.Eval("() => { try { return " + expr + " } catch(e) { return undefined } }")
		if err != nil {
			slog.Debug("JS pre-eval failed", "expression", expr, "error", err)
			continue
		}
		if result == nil || result.Value.Nil() {
			continue
		}
		val := result.Value.String()
		if val != "" {
			results[expr] = val
			slog.Debug("JS pre-eval success", "expression", expr, "value", val)
		}
	}
}

// probe404 navigates to a random non-existent path via the browser to capture
// the 404 response and evaluate JS expressions on the page.
func (s *Scanner) probe404(ctx context.Context, baseURL string, jsExprs []string) (*models.ChainedResponse, map[string]string, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	path := "/fp-" + hex.EncodeToString(b)
	probeURL := strings.TrimRight(baseURL, "/") + path

	return s.browserPool.NavigateCaptureAndEval(ctx, probeURL, jsExprs)
}

// Detectors returns the list of registered detectors (for /detections endpoint).
func (s *Scanner) Detectors() []models.Detector {
	return s.engine.Detectors()
}

// Close shuts down the scanner (browser pool, etc.).
func (s *Scanner) Close() {
	if s.browserPool != nil {
		s.browserPool.Close()
	}
}
