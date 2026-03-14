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
}

// New creates a centralized HTTP client.
// Redirects are only followed when the target host matches the original host.
func New(cfg Config) *http.Client {
	client := &http.Client{
		Timeout:       cfg.Timeout,
		CheckRedirect: sameHostOnly,
	}

	if cfg.ProxyURL != "" {
		proxyURL, err := url.Parse(cfg.ProxyURL)
		if err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}
		}
	}

	return client
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
