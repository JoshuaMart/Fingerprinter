package httpclient

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Config holds HTTP client settings.
type Config struct {
	Timeout  time.Duration
	ProxyURL string
	Headers  map[string]string
}

// New creates a centralized HTTP client.
// Redirects are only followed when the target host matches the original host.
func New(cfg Config) *http.Client {
	var base http.RoundTripper

	if cfg.ProxyURL != "" {
		proxyURL, err := url.Parse(cfg.ProxyURL)
		if err == nil {
			base = &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}
		}
	}

	if len(cfg.Headers) > 0 {
		base = &headerTransport{base: base, headers: cfg.Headers}
	}

	client := &http.Client{
		Timeout:       cfg.Timeout,
		CheckRedirect: sameHostOnly,
		Transport:     base,
	}

	return client
}

// headerTransport injects custom headers into every outgoing request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// NoRedirect returns a shallow copy of the client that never follows redirects.
// Used by the chain follower which handles redirects manually.
func NoRedirect(client *http.Client) *http.Client {
	c := *client
	c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &c
}

func sameHostOnly(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Host != via[0].URL.Host {
		return http.ErrUseLastResponse
	}
	return nil
}
