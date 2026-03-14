package yaml

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/twmb/murmur3"
)

// faviconMMH3 fetches the favicon and returns its Shodan-compatible mmh3 hash.
// Algorithm: fetch raw bytes → RFC 2045 base64 (76-char lines + trailing \n) → murmur3 32-bit → signed int32.
func faviconMMH3(client *http.Client, baseURL string, doc *goquery.Document) (int32, bool) {
	if client == nil || baseURL == "" {
		return 0, false
	}

	faviconURL := findFaviconURL(doc, strings.TrimRight(baseURL, "/"))
	if faviconURL == "" {
		return 0, false
	}

	body, err := fetchFavicon(client, faviconURL)
	if err != nil || len(body) == 0 {
		return 0, false
	}

	encoded := base64RFC2045(body)
	hash := murmur3.Sum32([]byte(encoded))

	return int32(hash), true
}

// findFaviconURL extracts the favicon URL from HTML link tags, falling back to /favicon.ico.
func findFaviconURL(doc *goquery.Document, baseURL string) string {
	if doc != nil {
		var href string
		doc.Find(`link[rel="icon"], link[rel="shortcut icon"]`).First().Each(func(_ int, s *goquery.Selection) {
			if h, exists := s.Attr("href"); exists && h != "" {
				href = h
			}
		})
		if href != "" {
			return resolveURL(href, baseURL)
		}
	}
	return baseURL + "/favicon.ico"
}

func resolveURL(href, baseURL string) string {
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if strings.HasPrefix(href, "/") {
		return baseURL + href
	}
	if !strings.HasPrefix(href, "http") {
		return baseURL + "/" + href
	}
	return href
}

func fetchFavicon(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	return io.ReadAll(resp.Body)
}

// base64RFC2045 encodes data as base64 with RFC 2045 line wrapping (76-char lines + trailing newline).
func base64RFC2045(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)

	var b strings.Builder
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		b.WriteString(encoded[i:end])
		b.WriteByte('\n')
	}
	return b.String()
}
