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

	"encoding/json"
	"io"

	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
	"golang.org/x/net/html"
)

// Pool manages browser pages connected to a remote browser (Chrome via CDP).
type Pool struct {
	browser     *rod.Browser
	controlURL  string
	proxyURL    string
	headers     map[string]string
	pageTimeout time.Duration
	mu          sync.Mutex
	closed      bool
}

// NewPool connects to a remote browser via CDP.
// If proxyURL is non-empty, a dedicated browser context is created so that
// all pages opened by the pool route traffic through the proxy.
func NewPool(_ int, pageTimeout time.Duration, controlURL, proxyURL string, headers map[string]string) (*Pool, error) {
	wsURL, err := resolveWSURL(controlURL)
	if err != nil {
		return nil, fmt.Errorf("resolving browser WebSocket URL from %s: %w", controlURL, err)
	}
	slog.Info("resolved browser WebSocket URL", "control", controlURL, "ws", wsURL)

	b := rod.New().ControlURL(wsURL)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("connecting to browser at %s: %w", wsURL, err)
	}

	// When a proxy is configured, create a dedicated browser context with the proxy.
	if proxyURL != "" {
		res, err := proto.TargetCreateBrowserContext{
			ProxyServer:     proxyURL,
			DisposeOnDetach: true,
		}.Call(b)
		if err != nil {
			return nil, fmt.Errorf("creating browser context with proxy: %w", err)
		}
		ctx := *b
		ctx.BrowserContextID = res.BrowserContextID
		b = &ctx
		slog.Info("browser proxy configured", "proxy", proxyURL)
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
		proxyURL:    proxyURL,
		headers:     headers,
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

	wsURL, err := resolveWSURL(p.controlURL)
	if err != nil {
		return fmt.Errorf("resolving browser WebSocket URL from %s: %w", p.controlURL, err)
	}

	b := rod.New().ControlURL(wsURL)
	if err := b.Connect(); err != nil {
		return fmt.Errorf("reconnecting to browser at %s: %w", wsURL, err)
	}

	if p.proxyURL != "" {
		res, err := proto.TargetCreateBrowserContext{
			ProxyServer:     p.proxyURL,
			DisposeOnDetach: true,
		}.Call(b)
		if err != nil {
			return fmt.Errorf("recreating browser context with proxy: %w", err)
		}
		ctx := *b
		ctx.BrowserContextID = res.BrowserContextID
		b = &ctx
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

	if err := p.setExtraHeaders(page); err != nil {
		slog.Warn("failed to set extra headers on page", "error", err)
	}

	return page, nil
}

// setExtraHeaders applies configured headers to the page via CDP.
// User-Agent requires a dedicated CDP call (Network.setUserAgentOverride).
func (p *Pool) setExtraHeaders(page *rod.Page) error {
	if len(p.headers) == 0 {
		return nil
	}

	hdrs := make(proto.NetworkHeaders)
	for k, v := range p.headers {
		if strings.EqualFold(k, "User-Agent") {
			if err := (proto.NetworkSetUserAgentOverride{UserAgent: v}).Call(page); err != nil {
				return fmt.Errorf("setting user-agent override: %w", err)
			}
			continue
		}
		hdrs[k] = gson.New(v)
	}

	if len(hdrs) > 0 {
		return proto.NetworkSetExtraHTTPHeaders{Headers: hdrs}.Call(page)
	}
	return nil
}

// NavigateResult holds the output of a browser navigation.
type NavigateResult struct {
	Page             *rod.Page
	ExternalHosts    []string
	WebSockets       []string
	Chain            []models.ChainedResponse
	ExternalRedirect bool
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
	capture := NewNetworkCapture(targetHost, targetURL, page.FrameID)

	// Listen for network events
	go page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			capture.HandleRequestWillBeSent(e)
		},
		func(e *proto.NetworkResponseReceived) {
			capture.HandleResponseReceived(e)
		},
		func(e *proto.NetworkWebSocketCreated) {
			capture.HandleWebSocketCreated(e)
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
	externalRedirect := false
	info, err := page.Info()
	if err == nil {
		if parsed, parseErr := url.Parse(info.URL); parseErr == nil {
			if parsed.Hostname() != targetHost {
				slog.Warn("browser redirected to external host",
					"from", targetHost, "to", parsed.Hostname())
				externalRedirect = true
			}
		}
	}

	// Build the chain from captured network events
	chainResponses, err := p.buildChain(page, capture)
	if err != nil {
		slog.Warn("failed to build full chain, using partial", "error", err)
	}

	// Truncate trailing out-of-scope responses
	if externalRedirect {
		chainResponses = truncateOutOfScope(chainResponses, targetHost)
		if len(chainResponses) == 0 {
			return fmt.Errorf("redirected to external host with no in-scope responses")
		}
	}

	result := &NavigateResult{
		Page:             page,
		ExternalHosts:    capture.ExternalHosts(),
		WebSockets:       capture.WebSockets(),
		Chain:            chainResponses,
		ExternalRedirect: externalRedirect,
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

	capture := NewNetworkCapture(targetHost, targetURL, page.FrameID)

	go page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			capture.HandleRequestWillBeSent(e)
		},
		func(e *proto.NetworkResponseReceived) {
			capture.HandleResponseReceived(e)
		},
		func(e *proto.NetworkWebSocketCreated) {
			capture.HandleWebSocketCreated(e)
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

// NavigateCaptureAndEval navigates to a URL, captures the response, waits for
// network idle, and evaluates JS expressions on the page.
func (p *Pool) NavigateCaptureAndEval(ctx context.Context, targetURL string, jsExprs []string) (*models.ChainedResponse, map[string]string, error) {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing URL %s: %w", targetURL, err)
	}
	targetHost := parsedURL.Hostname()

	page, err := p.createPage()
	if err != nil {
		return nil, nil, fmt.Errorf("getting page: %w", err)
	}
	defer func() { _ = page.Close() }()

	page = page.Context(ctx).Timeout(p.pageTimeout)

	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return nil, nil, fmt.Errorf("enabling network domain: %w", err)
	}

	capture := NewNetworkCapture(targetHost, targetURL, page.FrameID)

	go page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			capture.HandleRequestWillBeSent(e)
		},
		func(e *proto.NetworkResponseReceived) {
			capture.HandleResponseReceived(e)
		},
		func(e *proto.NetworkWebSocketCreated) {
			capture.HandleWebSocketCreated(e)
		},
	)()

	if err := page.Navigate(targetURL); err != nil {
		return nil, nil, fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	if err := page.WaitLoad(); err != nil {
		slog.Warn("page WaitLoad failed", "url", targetURL, "error", err)
	}

	// Wait for network to settle (scripts, XHR, etc.) before evaluating JS
	wait := page.WaitRequestIdle(time.Second, nil, nil, nil)
	wait()

	chain := capture.Chain()
	if len(chain) == 0 {
		return nil, nil, fmt.Errorf("no network response captured for %s", targetURL)
	}

	last := chain[len(chain)-1]
	flat := flattenHeaders(last.headers)

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

	// Evaluate JS expressions on the navigated page
	jsResults := make(map[string]string, len(jsExprs))
	for _, expr := range jsExprs {
		result, err := page.Eval("() => { try { return " + expr + " } catch(e) { return undefined } }")
		if err != nil {
			continue
		}
		if result == nil || result.Value.Nil() {
			continue
		}
		val := result.Value.String()
		if val == "" || val == "false" {
			continue
		}
		jsResults[expr] = val
	}

	return resp, jsResults, nil
}

// truncateOutOfScope removes trailing responses whose hostname differs from targetHost.
func truncateOutOfScope(chain []models.ChainedResponse, targetHost string) []models.ChainedResponse {
	for len(chain) > 0 {
		last := chain[len(chain)-1]
		if parsed, err := url.Parse(last.URL); err == nil && parsed.Hostname() == targetHost {
			break
		}
		chain = chain[:len(chain)-1]
	}
	return chain
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

// resolveWSURL fetches /json/version from the CDP endpoint to get the full
// WebSocket debugger URL. Chrome requires Host to be localhost or an IP,
// so we override the Host header in the request.
func resolveWSURL(controlURL string) (string, error) {
	parsed, err := url.Parse(controlURL)
	if err != nil {
		return "", fmt.Errorf("parsing control URL: %w", err)
	}

	if parsed.Scheme == "ws" || parsed.Scheme == "wss" {
		return controlURL, nil
	}

	endpoint := controlURL + "/json/version"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	// Chrome rejects /json/version when Host is not localhost or an IP
	req.Host = "localhost"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching /json/version: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading /json/version response: %w", err)
	}

	var info struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("parsing /json/version: %w", err)
	}
	if info.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("/json/version did not return webSocketDebuggerUrl")
	}

	// Replace the host (Chrome returns localhost) with the actual control host
	wsURL, err := url.Parse(info.WebSocketDebuggerURL)
	if err != nil {
		return "", fmt.Errorf("parsing WebSocket URL: %w", err)
	}
	wsURL.Host = parsed.Host

	return wsURL.String(), nil
}

// Close marks the pool as closed. Does not close the remote browser — its
// lifecycle is managed by the Docker container.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}
