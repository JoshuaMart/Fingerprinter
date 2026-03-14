package scanner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/JoshuaMart/fingerprinter/internal/browser"
	"github.com/JoshuaMart/fingerprinter/internal/chain"
	"github.com/JoshuaMart/fingerprinter/internal/config"
	"github.com/JoshuaMart/fingerprinter/internal/detection/detectors"
	"github.com/JoshuaMart/fingerprinter/internal/detection/engine"
	yamldet "github.com/JoshuaMart/fingerprinter/internal/detection/yaml"
	"github.com/JoshuaMart/fingerprinter/internal/httpclient"
	"github.com/JoshuaMart/fingerprinter/internal/metadata"
	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/go-rod/rod"
)

// Scanner orchestrates the full scan pipeline.
type Scanner struct {
	cfg         *config.Config
	httpClient  *http.Client
	engine      *engine.Engine
	browserPool *browser.Pool
	semaphore   chan struct{}
}

// New creates a Scanner, loads detections, and optionally starts the browser pool.
func New(cfg *config.Config) (*Scanner, error) {
	s := &Scanner{
		cfg: cfg,
		httpClient: httpclient.New(httpclient.Config{
			Timeout:  cfg.Scanner.RequestTimeout,
			ProxyURL: cfg.Scanner.Proxy,
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

	// Start browser pool if enabled
	if cfg.Browser.Enabled {
		pool, err := browser.NewPool(cfg.Browser.PoolSize, cfg.Browser.PageTimeout)
		if err != nil {
			return nil, fmt.Errorf("starting browser pool: %w", err)
		}
		s.browserPool = pool
	}

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

	opts := req.Options
	if opts == nil {
		opts = &models.ScanOptions{}
	}

	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = s.cfg.Scanner.MaxRedirects
	}

	// Step 1 — HTTP chain
	chainCfg := chain.Config{
		MaxRedirects: maxRedirects,
		Headers:      s.cfg.Scanner.Headers,
	}
	responses, err := chain.Follow(ctx, req.URL, chainCfg, s.httpClient)
	if err != nil {
		return nil, fmt.Errorf("HTTP chain: %w", err)
	}

	if len(responses) == 0 {
		return nil, fmt.Errorf("no responses received")
	}

	finalResp := &responses[len(responses)-1]
	baseURL := finalResp.URL

	// Step 2 — Parse DOM
	doc, err := chain.ParseDocument(finalResp)
	if err != nil {
		slog.Warn("DOM parsing failed, continuing without document", "error", err)
	}

	// Step 3 — Browser (optional)
	var browserPage *rod.Page
	var externalHosts []string

	useBrowser := opts.BrowserDetection && s.browserPool != nil
	if useBrowser {
		err := s.browserPool.Navigate(ctx, baseURL, func(result *browser.NavigateResult) error {
			browserPage = result.Page
			externalHosts = result.ExternalHosts
			return nil
		})
		if err != nil {
			slog.Warn("browser navigation failed, continuing without browser", "error", err)
		}
	}

	// Step 4 — 404 probe (for detection only, not included in output chain)
	detResponses := make([]models.ChainedResponse, len(responses))
	copy(detResponses, responses)
	if notFound, err := s.probe404(ctx, baseURL); err == nil {
		detResponses = append(detResponses, *notFound)
	}

	// Step 5 — Detections (parallel, handled by engine)
	detCtx := &models.DetectionContext{
		Responses:   detResponses,
		Document:    doc,
		HTTPClient:  s.httpClient,
		Browser:     nil,
		BrowserPage: browserPage,
		BaseURL:     baseURL,
	}
	if s.browserPool != nil {
		detCtx.Browser = s.browserPool.Browser()
	}

	technologies := s.engine.Run(detCtx)

	// Step 6 — Metadata
	cookies := chain.ExtractCookies(responses)
	scanMeta := metadata.Fetch(s.httpClient, baseURL, doc)

	// Step 7 — Aggregate
	result := &models.ScanResult{
		URL:           req.URL,
		Chain:         responses,
		Technologies:  technologies,
		Cookies:       cookies,
		Metadata:      scanMeta,
		ExternalHosts: externalHosts,
		ScannedAt:     time.Now().UTC(),
	}

	return result, nil
}

// probe404 requests a random non-existent path to capture the 404 response for detection analysis.
func (s *Scanner) probe404(ctx context.Context, baseURL string) (*models.ChainedResponse, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	path := "/fp-" + hex.EncodeToString(b)

	probeURL := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range s.cfg.Scanner.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &models.ChainedResponse{
		URL:        probeURL,
		StatusCode: resp.StatusCode,
		Headers:    chain.FlattenHeaders(resp.Header),
		RawHeaders: resp.Header,
		Body:       body,
	}, nil
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
