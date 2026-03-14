package chain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/JoshuaMart/fingerprinter/internal/httpclient"
	"github.com/JoshuaMart/fingerprinter/internal/models"
	"golang.org/x/net/html"
)

var (
	ErrMaxRedirects     = errors.New("maximum redirects exceeded")
	ErrCircularRedirect = errors.New("circular redirect detected")
	ErrMissingLocation  = errors.New("3xx response without Location header")
)

// Config holds settings for the HTTP chain follower.
type Config struct {
	MaxRedirects int
	Headers      map[string]string
}

// Follow follows HTTP redirects from the given URL, capturing each hop.
func Follow(ctx context.Context, targetURL string, cfg Config, baseClient *http.Client) ([]models.ChainedResponse, error) {
	if err := ValidateURL(targetURL); err != nil {
		return nil, err
	}

	client := httpclient.NoRedirect(baseClient)

	var responses []models.ChainedResponse
	visited := make(map[string]bool)
	currentURL := targetURL

	for i := 0; i <= cfg.MaxRedirects; i++ {
		if visited[currentURL] {
			return responses, ErrCircularRedirect
		}
		visited[currentURL] = true

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return responses, fmt.Errorf("creating request for %s: %w", currentURL, err)
		}

		for k, v := range cfg.Headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			return responses, fmt.Errorf("requesting %s: %w", currentURL, err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return responses, fmt.Errorf("reading body for %s: %w", currentURL, err)
		}

		hop := models.ChainedResponse{
			URL:          currentURL,
			StatusCode:   resp.StatusCode,
			Headers:      FlattenHeaders(resp.Header),
			RawHeaders:   resp.Header,
			Body:         body,
			ResponseSize: len(body),
		}

		if isHTML(resp.Header.Get("Content-Type")) {
			hop.Title = parseTitle(body)
		}

		responses = append(responses, hop)

		if !isRedirect(resp.StatusCode) {
			return responses, nil
		}

		location := resp.Header.Get("Location")
		if location == "" {
			return responses, ErrMissingLocation
		}

		nextURL, err := req.URL.Parse(location)
		if err != nil {
			return responses, fmt.Errorf("parsing redirect location %q: %w", location, err)
		}
		currentURL = nextURL.String()
	}

	return responses, ErrMaxRedirects
}

// ValidateURL checks that the URL scheme is HTTP(S) and the hostname is not empty.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("blocked scheme %q: only http and https are allowed", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("empty hostname")
	}
	return nil
}

// FlattenHeaders converts http.Header to a flat map (values joined with ", ").
func FlattenHeaders(h http.Header) map[string]string {
	flat := make(map[string]string, len(h))
	for k, v := range h {
		flat[k] = strings.Join(v, ", ")
	}
	return flat
}

func isRedirect(code int) bool {
	return code >= 300 && code < 400
}

func isHTML(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/html")
}

func parseTitle(body []byte) *string {
	tokenizer := html.NewTokenizer(strings.NewReader(string(body)))
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
