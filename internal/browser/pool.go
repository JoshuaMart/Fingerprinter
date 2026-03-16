package browser

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"golang.org/x/net/html"
)

// Pool manages browser pages connected to a remote browser (Chrome via CDP).
type Pool struct {
	browser     *rod.Browser
	controlURL  string
	pageTimeout time.Duration
	mu          sync.Mutex
	closed      bool
}

// NewPool connects to a remote browser via CDP.
func NewPool(_ int, pageTimeout time.Duration, controlURL string) (*Pool, error) {
	b := rod.New().ControlURL(controlURL)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("connecting to browser at %s: %w", controlURL, err)
	}

	// Health check — open and close a blank page
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("browser health check failed: %w", err)
	}
	_ = page.Close()

	return &Pool{
		browser:     b,
		controlURL:  controlURL,
		pageTimeout: pageTimeout,
	}, nil
}

// reconnect drops the existing browser connection and creates a new one.
func (p *Pool) reconnect() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Close the old browser connection to stop its goroutines (consumeMessages, etc.)
	if p.browser != nil {
		_ = p.browser.Close()
	}

	b := rod.New().ControlURL(p.controlURL)
	if err := b.Connect(); err != nil {
		return fmt.Errorf("reconnecting to browser at %s: %w", p.controlURL, err)
	}
	p.browser = b
	return nil
}

// createPage creates a new blank page, reconnecting if necessary.
func (p *Pool) createPage() (*rod.Page, error) {
	page, err := p.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		slog.Warn("page creation failed, attempting reconnect", "error", err)
		if reconnErr := p.reconnect(); reconnErr != nil {
			return nil, fmt.Errorf("reconnect failed: %w (original: %w)", reconnErr, err)
		}
		page, err = p.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			return nil, fmt.Errorf("creating page after reconnect: %w", err)
		}
	}
	return page, nil
}

// NavigateResult holds the output of a browser navigation.
type NavigateResult struct {
	Page          *rod.Page
	ExternalHosts []string
	Chain         []models.ChainedResponse
}

// Navigate opens a URL in a fresh page, captures the redirect chain via CDP Network
// events, waits for load, and calls fn with the result. The page is closed after fn returns.
func (p *Pool) Navigate(ctx context.Context, targetURL string, fn func(result *NavigateResult) error) error {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("parsing URL %s: %w", targetURL, err)
	}
	targetHost := parsedURL.Hostname()

	page, err := p.createPage()
	if err != nil {
		return fmt.Errorf("getting page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Context(ctx).Timeout(p.pageTimeout)

	// Enable Network domain for CDP event capture
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return fmt.Errorf("enabling network domain: %w", err)
	}

	// Set up network capture
	capture := NewNetworkCapture(targetHost, targetURL)

	// Listen for network events
	go page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			capture.HandleRequestWillBeSent(e)
		},
		func(e *proto.NetworkResponseReceived) {
			capture.HandleResponseReceived(e)
		},
	)()

	if err := page.Navigate(targetURL); err != nil {
		return fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	// Wait for page to be ready
	if err := page.WaitLoad(); err != nil {
		slog.Warn("page WaitLoad failed, continuing", "url", targetURL, "error", err)
	}

	// Wait for network to settle (scripts, XHR, etc.)
	wait := page.WaitRequestIdle(time.Second, nil, nil, nil)
	wait()

	// Check for client-side redirect to a different host
	info, err := page.Info()
	if err == nil {
		if parsed, parseErr := url.Parse(info.URL); parseErr == nil {
			if parsed.Hostname() != targetHost {
				slog.Warn("browser redirected to external host, ignoring",
					"from", targetHost, "to", parsed.Hostname())
				return nil
			}
		}
	}

	// Build the chain from captured network events
	chainResponses, err := p.buildChain(page, capture)
	if err != nil {
		slog.Warn("failed to build full chain, using partial", "error", err)
	}

	result := &NavigateResult{
		Page:          page,
		ExternalHosts: capture.ExternalHosts(),
		Chain:         chainResponses,
	}

	return fn(result)
}

// NavigateAndCapture navigates to a URL in a new tab and returns the final response.
// Used by detectors for path checks and probe404.
func (p *Pool) NavigateAndCapture(ctx context.Context, targetURL string) (*models.ChainedResponse, error) {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL %s: %w", targetURL, err)
	}
	targetHost := parsedURL.Hostname()

	page, err := p.createPage()
	if err != nil {
		return nil, fmt.Errorf("getting page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Context(ctx).Timeout(p.pageTimeout)

	// Enable Network domain
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return nil, fmt.Errorf("enabling network domain: %w", err)
	}

	capture := NewNetworkCapture(targetHost, targetURL)

	go page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			capture.HandleRequestWillBeSent(e)
		},
		func(e *proto.NetworkResponseReceived) {
			capture.HandleResponseReceived(e)
		},
	)()

	if err := page.Navigate(targetURL); err != nil {
		return nil, fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	if err := page.WaitLoad(); err != nil {
		slog.Warn("page WaitLoad failed", "url", targetURL, "error", err)
	}

	chain := capture.Chain()
	if len(chain) == 0 {
		return nil, fmt.Errorf("no network response captured for %s", targetURL)
	}

	last := chain[len(chain)-1]
	flat := flattenHeaders(last.headers)

	// Try to get the response body
	var body []byte
	if last.requestID != "" {
		bodyResult, err := proto.NetworkGetResponseBody{RequestID: last.requestID}.Call(page)
		if err == nil {
			body = []byte(bodyResult.Body)
		}
	}

	resp := &models.ChainedResponse{
		URL:          last.url,
		StatusCode:   last.statusCode,
		Headers:      flat,
		RawHeaders:   last.headers,
		Body:         body,
		ResponseSize: len(body),
	}

	if isHTMLContentType(flat["Content-Type"]) {
		resp.Title = parseTitle(body)
	}

	return resp, nil
}

// buildChain converts captured network events into []models.ChainedResponse.
// It also fetches the body of the final response.
func (p *Pool) buildChain(page *rod.Page, capture *NetworkCapture) ([]models.ChainedResponse, error) {
	captured := capture.Chain()
	if len(captured) == 0 {
		return nil, fmt.Errorf("no responses captured")
	}

	responses := make([]models.ChainedResponse, 0, len(captured))

	for i, c := range captured {
		flat := flattenHeaders(c.headers)
		resp := models.ChainedResponse{
			URL:        c.url,
			StatusCode: c.statusCode,
			Headers:    flat,
			RawHeaders: c.headers,
		}

		// Only fetch body for the final response (and only if we have a requestID)
		if i == len(captured)-1 && c.requestID != "" {
			bodyResult, err := proto.NetworkGetResponseBody{RequestID: c.requestID}.Call(page)
			if err == nil {
				resp.Body = []byte(bodyResult.Body)
				resp.ResponseSize = len(resp.Body)
			}

			if isHTMLContentType(flat["Content-Type"]) {
				resp.Title = parseTitle(resp.Body)
			}
		}

		responses = append(responses, resp)
	}

	return responses, nil
}

// flattenHeaders converts http.Header to a flat map (values joined with ", ").
func flattenHeaders(h http.Header) map[string]string {
	flat := make(map[string]string, len(h))
	for k, v := range h {
		flat[k] = strings.Join(v, ", ")
	}
	return flat
}

func isHTMLContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/html")
}

func parseTitle(body []byte) *string {
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	inTitle := false
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return nil
		case html.StartTagToken:
			tn, _ := tokenizer.TagName()
			if string(tn) == "title" {
				inTitle = true
			}
		case html.TextToken:
			if inTitle {
				text := strings.TrimSpace(tokenizer.Token().Data)
				if text != "" {
					return &text
				}
			}
		case html.EndTagToken:
			if inTitle {
				return nil
			}
		}
	}
}

// EvalJS evaluates a JavaScript expression on the given page and returns the result as a string.
func EvalJS(page *rod.Page, expression string) (string, error) {
	obj, err := page.Eval("() => " + expression)
	if err != nil {
		return "", err
	}
	if obj == nil || obj.Value.Nil() {
		return "", nil
	}
	return obj.Value.String(), nil
}

// ExtractDOM extracts visible text and structural elements from the page.
func ExtractDOM(page *rod.Page) (string, error) {
	result, err := page.Eval(`() => {
		const sel = 'h1, h2, h3, h4, h5, h6, p, li, a, nav, header, footer, meta[name="description"]';
		const elements = document.querySelectorAll(sel);
		const parts = [];
		elements.forEach(el => {
			const tag = el.tagName.toLowerCase();
			const text = el.innerText ? el.innerText.trim() : '';
			if (tag === 'meta') {
				parts.push('meta-description: ' + (el.content || ''));
			} else if (text) {
				parts.push(tag + ': ' + text);
			}
		});
		return parts.join('\n');
	}`)
	if err != nil {
		return "", fmt.Errorf("extracting DOM: %w", err)
	}

	return result.Value.String(), nil
}

// Close marks the pool as closed. Does not close the remote browser — its
// lifecycle is managed by the Docker container.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}
