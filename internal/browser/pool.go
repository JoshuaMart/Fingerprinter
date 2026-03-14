package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Pool manages a pool of Rod browser pages.
type Pool struct {
	browser     *rod.Browser
	pagePool    rod.Pool[rod.Page]
	pageTimeout time.Duration
	mu          sync.Mutex
	closed      bool
}

// NewPool creates and starts a browser pool.
func NewPool(poolSize int, pageTimeout time.Duration) (*Pool, error) {
	l := launcher.New().Headless(true).NoSandbox(true)
	if bin := os.Getenv("CHROME_PATH"); bin != "" {
		l = l.Bin(bin)
	}
	u, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("launching browser: %w", err)
	}

	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("connecting to browser: %w", err)
	}

	pool := rod.NewPagePool(poolSize)

	return &Pool{
		browser:     b,
		pagePool:    pool,
		pageTimeout: pageTimeout,
	}, nil
}

// Browser returns the underlying Rod browser instance.
func (p *Pool) Browser() *rod.Browser {
	return p.browser
}

// NavigateResult holds the output of a browser navigation.
type NavigateResult struct {
	Page          *rod.Page
	ExternalHosts []string
}

// Navigate opens a URL in a pooled page, waits for load, and calls fn with the result.
// It monitors network requests to collect external hosts and verifies that any
// client-side redirect stays on the same host.
func (p *Pool) Navigate(ctx context.Context, targetURL string, fn func(result *NavigateResult) error) error {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("parsing URL %s: %w", targetURL, err)
	}
	targetHost := parsedURL.Hostname()

	create := func() (*rod.Page, error) {
		return p.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	}

	page, err := p.pagePool.Get(create)
	if err != nil {
		return fmt.Errorf("getting page from pool: %w", err)
	}
	defer p.pagePool.Put(page)

	page = page.Context(ctx).Timeout(p.pageTimeout)

	// Start monitoring external hosts via request hijacking
	monitor := newExternalHostMonitor(page, targetHost)
	defer monitor.stop()

	if err := page.Navigate(targetURL); err != nil {
		return fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	if err := page.WaitStable(500 * time.Millisecond); err != nil {
		slog.Warn("page did not stabilize, continuing", "url", targetURL, "error", err)
	}

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

	result := &NavigateResult{
		Page:          page,
		ExternalHosts: monitor.hosts(),
	}

	return fn(result)
}

// externalHostMonitor intercepts all requests to collect external hostnames.
type externalHostMonitor struct {
	mu     sync.Mutex
	seen   map[string]struct{}
	router *rod.HijackRouter
}

func newExternalHostMonitor(page *rod.Page, targetHost string) *externalHostMonitor {
	m := &externalHostMonitor{
		seen: make(map[string]struct{}),
	}

	m.router = page.HijackRequests()
	m.router.MustAdd("*", func(ctx *rod.Hijack) {
		parsed, err := url.Parse(ctx.Request.URL().String())
		if err == nil {
			host := parsed.Hostname()
			if host != "" && host != targetHost {
				m.mu.Lock()
				m.seen[host] = struct{}{}
				m.mu.Unlock()
			}
		}
		ctx.ContinueRequest(&proto.FetchContinueRequest{})
	})
	go m.router.Run()

	return m
}

func (m *externalHostMonitor) stop() {
	_ = m.router.Stop()
}

func (m *externalHostMonitor) hosts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	hosts := make([]string, 0, len(m.seen))
	for h := range m.seen {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts
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

// Screenshot captures a screenshot of the page with a 1280x800 viewport.
func Screenshot(page *rod.Page) ([]byte, error) {
	err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:  1280,
		Height: 800,
	})
	if err != nil {
		return nil, fmt.Errorf("setting viewport: %w", err)
	}

	data, err := page.Screenshot(true, &proto.PageCaptureScreenshot{
		Format: proto.PageCaptureScreenshotFormatPng,
	})
	if err != nil {
		return nil, fmt.Errorf("capturing screenshot: %w", err)
	}

	return data, nil
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

// Close shuts down the browser pool and the browser instance.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true

	p.pagePool.Cleanup(func(page *rod.Page) {
		_ = page.Close()
	})

	if err := p.browser.Close(); err != nil {
		slog.Error("failed to close browser", "error", err)
	}
}
