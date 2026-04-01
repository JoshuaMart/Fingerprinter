package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"encoding/json"
	"io"

	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
	"golang.org/x/net/html"
)

// browserBackend represents a single remote Chrome instance.
type browserBackend struct {
	browser    *rod.Browser
	controlURL string
	proxyURL   string
	mu         sync.Mutex
}

// reconnect drops the existing connection and creates a new one.
func (be *browserBackend) reconnect() error {
	be.mu.Lock()
	defer be.mu.Unlock()

	if be.browser != nil {
		_ = be.browser.Close()
	}

	wsURL, err := resolveWSURL(be.controlURL)
	if err != nil {
		return fmt.Errorf("resolving browser WebSocket URL from %s: %w", be.controlURL, err)
	}

	b, err := connectBrowser(wsURL)
	if err != nil {
		return fmt.Errorf("reconnecting to browser at %s: %w", wsURL, err)
	}

	if be.proxyURL != "" {
		res, err := proto.TargetCreateBrowserContext{
			ProxyServer:     be.proxyURL,
			DisposeOnDetach: true,
		}.Call(b)
		if err != nil {
			return fmt.Errorf("recreating browser context with proxy: %w", err)
		}
		ctx := *b
		ctx.BrowserContextID = res.BrowserContextID
		b = &ctx
	}

	be.browser = b
	return nil
}

// Pool manages browser pages distributed across one or more remote Chrome instances.
type Pool struct {
	backends      []*browserBackend
	headers       map[string]string
	pageTimeout   time.Duration
	pageSemaphore chan struct{} // limits concurrent open pages
	robin         atomic.Uint64
	closed        bool
}

// NewPool connects to one or more remote browsers via CDP.
// Pages are distributed across backends via round-robin.
func NewPool(maxPages int, pageTimeout time.Duration, controlURLs []string, proxyURL string, headers map[string]string) (*Pool, error) {
	if maxPages < 1 {
		maxPages = 10
	}
	if len(controlURLs) == 0 {
		return nil, fmt.Errorf("at least one browser control URL is required")
	}

	backends := make([]*browserBackend, 0, len(controlURLs))
	for _, controlURL := range controlURLs {
		wsURL, err := resolveWSURL(controlURL)
		if err != nil {
			return nil, fmt.Errorf("resolving browser WebSocket URL from %s: %w", controlURL, err)
		}
		slog.Info("resolved browser WebSocket URL", "control", controlURL, "ws", wsURL)

		b, err := connectBrowser(wsURL)
		if err != nil {
			return nil, fmt.Errorf("connecting to browser at %s: %w", wsURL, err)
		}

		// When a proxy is configured, create a dedicated browser context with the proxy.
		if proxyURL != "" {
			res, err := proto.TargetCreateBrowserContext{
				ProxyServer:     proxyURL,
				DisposeOnDetach: true,
			}.Call(b)
			if err != nil {
				return nil, fmt.Errorf("creating browser context with proxy on %s: %w", controlURL, err)
			}
			ctx := *b
			ctx.BrowserContextID = res.BrowserContextID
			b = &ctx
			slog.Info("browser proxy configured", "proxy", proxyURL, "backend", controlURL)
		}

		// Health check
		page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			return nil, fmt.Errorf("browser health check failed for %s: %w", controlURL, err)
		}
		_ = page.Close()

		backends = append(backends, &browserBackend{
			browser:    b,
			controlURL: controlURL,
			proxyURL:   proxyURL,
		})
	}

	slog.Info("browser pool initialized", "backends", len(backends), "max_pages", maxPages)

	return &Pool{
		backends:      backends,
		headers:       headers,
		pageTimeout:   pageTimeout,
		pageSemaphore: make(chan struct{}, maxPages),
	}, nil
}

// nextBackend returns the next backend via round-robin.
func (p *Pool) nextBackend() *browserBackend {
	idx := p.robin.Add(1) - 1
	return p.backends[idx%uint64(len(p.backends))]
}

// createPage creates a new blank page on a round-robin backend, reconnecting if necessary.
// It acquires the page semaphore first to limit concurrent pages.
// Callers MUST call releasePage() when done (typically via defer).
func (p *Pool) createPage(ctx context.Context) (*rod.Page, error) {
	select {
	case p.pageSemaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	be := p.nextBackend()
	page, err := be.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		slog.Warn("page creation failed, attempting reconnect", "error", err, "backend", be.controlURL)
		if reconnErr := be.reconnect(); reconnErr != nil {
			p.releasePage()
			return nil, fmt.Errorf("reconnect failed: %w (original: %w)", reconnErr, err)
		}
		page, err = be.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			p.releasePage()
			return nil, fmt.Errorf("creating page after reconnect: %w", err)
		}
	}

	if err := p.setExtraHeaders(page); err != nil {
		slog.Warn("failed to set extra headers on page", "error", err)
	}

	return page, nil
}

// releasePage releases a page slot back to the semaphore.
func (p *Pool) releasePage() {
	<-p.pageSemaphore
}

// createIncognitoContext creates a fresh BrowserContext on a round-robin backend
// for scan isolation. The returned cleanup function disposes the context.
func (p *Pool) createIncognitoContext() (*rod.Browser, func(), error) {
	be := p.nextBackend()
	create := proto.TargetCreateBrowserContext{DisposeOnDetach: true}
	if be.proxyURL != "" {
		create.ProxyServer = be.proxyURL
	}

	res, err := create.Call(be.browser)
	if err != nil {
		return nil, nil, fmt.Errorf("creating browser context: %w", err)
	}

	incognito := *be.browser
	incognito.BrowserContextID = res.BrowserContextID

	cleanup := func() {
		_ = proto.TargetDisposeBrowserContext{BrowserContextID: res.BrowserContextID}.Call(be.browser)
	}
	return &incognito, cleanup, nil
}

// createPageOn creates a new blank page on the given browser (or context),
// acquiring the page semaphore first.
func (p *Pool) createPageOn(ctx context.Context, b *rod.Browser) (*rod.Page, error) {
	select {
	case p.pageSemaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		p.releasePage()
		return nil, fmt.Errorf("creating page: %w", err)
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
	BrowserCookies   map[string]string // Cookies from browser cookie jar (name → value)
}

// Navigate opens a URL in a fresh page, captures the redirect chain via CDP Network
// events, waits for load, and calls fn with the result. The page is closed after fn returns.
// Each call gets its own incognito BrowserContext for cookie/storage isolation.
func (p *Pool) Navigate(ctx context.Context, targetURL string, fn func(result *NavigateResult) error) error {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("parsing URL %s: %w", targetURL, err)
	}
	targetHost := parsedURL.Hostname()

	// Create an isolated incognito context for this scan (clean cookies/storage).
	// If a proxy is configured, apply it to the context as well.
	incognito, cleanup, err := p.createIncognitoContext()
	if err != nil {
		return fmt.Errorf("creating incognito context: %w", err)
	}
	defer cleanup()

	page, err := p.createPageOn(ctx, incognito)
	if err != nil {
		return fmt.Errorf("getting page: %w", err)
	}
	defer func() { _ = page.Close(); p.releasePage() }()

	page = page.Context(ctx)

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

	// Use page timeout only for navigation + wait phases
	navPage := page.Timeout(p.pageTimeout)

	if err := navPage.Navigate(targetURL); err != nil {
		return fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	// Wait for page to be ready using a two-phase adaptive strategy:
	// 1. WaitLoad ensures scripts/subresources are loaded (baseline)
	// 2. In parallel: short network idle (200ms grace) + MutationObserver-based
	//    DOM stability (150ms of no mutations). Both must be satisfied.
	// This is faster than fixed-duration waits on static sites while still
	// catching SPA hydration and async data fetching.
	if err := navPage.WaitLoad(); err != nil {
		slog.Warn("page WaitLoad failed, continuing", "url", targetURL, "error", err)
	}
	waitPageReady(navPage)

	// Post-navigation operations use the parent context (not the page timeout)
	// so they don't fail if navigation consumed the entire page_timeout budget.

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

	// Extract cookies from browser cookie jar (CDP doesn't expose Set-Cookie headers)
	browserCookies := make(map[string]string)
	cookies, err := page.Cookies(nil)
	if err != nil {
		slog.Warn("failed to extract browser cookies", "error", err)
	} else {
		slog.Debug("browser cookies extracted", "count", len(cookies))
		for _, c := range cookies {
			browserCookies[c.Name] = c.Value
		}
	}

	result := &NavigateResult{
		Page:             page,
		ExternalHosts:    capture.ExternalHosts(),
		WebSockets:       capture.WebSockets(),
		Chain:            chainResponses,
		ExternalRedirect: externalRedirect,
		BrowserCookies:   browserCookies,
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

	page, err := p.createPage(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting page: %w", err)
	}
	defer func() { _ = page.Close(); p.releasePage() }()

	page = page.Context(ctx)

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

	navPage := page.Timeout(p.pageTimeout)

	if err := navPage.Navigate(targetURL); err != nil {
		return nil, fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	if err := navPage.WaitLoad(); err != nil {
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

	page, err := p.createPage(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("getting page: %w", err)
	}
	defer func() { _ = page.Close(); p.releasePage() }()

	page = page.Context(ctx)

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

	navPage := page.Timeout(p.pageTimeout)

	if err := navPage.Navigate(targetURL); err != nil {
		return nil, nil, fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	if err := navPage.WaitLoad(); err != nil {
		slog.Warn("page WaitLoad failed", "url", targetURL, "error", err)
	}
	waitPageReady(navPage)

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

// waitPageReady waits for both network quiet and DOM stability in parallel.
// It returns when both conditions are met or the page's context expires.
func waitPageReady(page *rod.Page) {
	done := make(chan struct{}, 2)

	// Signal 1: network idle for 200ms
	go func() {
		wait := page.WaitRequestIdle(200*time.Millisecond, nil, nil, nil)
		wait()
		done <- struct{}{}
	}()

	// Signal 2: DOM stability via MutationObserver (no mutations for 150ms)
	go func() {
		_, err := page.Eval(domStableScript)
		if err != nil {
			slog.Debug("DOM stability check failed, skipping", "error", err)
		}
		done <- struct{}{}
	}()

	// Wait for both signals
	<-done
	<-done
}

// domStableScript injects a MutationObserver that resolves when the DOM has
// been quiet (no childList/subtree mutations) for 150ms. A 5s hard cap
// prevents infinite waits on sites with continuous DOM mutations (tickers,
// animations, live feeds).
const domStableScript = `() => new Promise(resolve => {
	const QUIET = 150, CAP = 5000;
	let timer = setTimeout(resolve, QUIET);
	const cap = setTimeout(() => { observer.disconnect(); resolve(); }, CAP);
	const observer = new MutationObserver(() => {
		clearTimeout(timer);
		timer = setTimeout(() => { observer.disconnect(); clearTimeout(cap); resolve(); }, QUIET);
	});
	const target = document.body || document.documentElement;
	if (target) {
		observer.observe(target, { childList: true, subtree: true });
	} else {
		clearTimeout(cap);
		resolve();
	}
})`

// connectBrowser creates a Rod browser connected to the given WebSocket URL.
// For wss:// URLs (e.g. Browserless cloud), we create the CDP client manually
// with a valid Sec-WebSocket-Key header, because Rod's default implementation
// sends a literal "nil" key that some proxies reject.
func connectBrowser(wsURL string) (*rod.Browser, error) {
	b := rod.New()

	parsed, _ := url.Parse(wsURL)
	if parsed != nil && (parsed.Scheme == "wss" || parsed.Scheme == "ws") {
		key := make([]byte, 16)
		_, _ = rand.Read(key)
		header := http.Header{
			"Sec-WebSocket-Key": {base64.StdEncoding.EncodeToString(key)},
		}
		client, err := cdp.StartWithURL(context.Background(), wsURL, header)
		if err != nil {
			return nil, err
		}
		b = b.Client(client)
	} else {
		b = b.ControlURL(wsURL)
	}

	if err := b.Connect(); err != nil {
		return nil, err
	}
	return b, nil
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

// Close marks the pool as closed. Does not close remote browsers — their
// lifecycle is managed by the Docker containers.
func (p *Pool) Close() {
	p.closed = true
}
