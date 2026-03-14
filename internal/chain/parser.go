package chain

import (
	"bytes"
	"strings"

	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/PuerkitoBio/goquery"
)

// ParseDocument parses the final response body into a goquery Document.
func ParseDocument(resp *models.ChainedResponse) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(bytes.NewReader(resp.Body))
}

// ExtractMeta extracts meta tags from a goquery Document.
// Returns a map where keys are the meta name or property, and values are the content.
func ExtractMeta(doc *goquery.Document) map[string]string {
	metas := make(map[string]string)
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		var key string
		if name, exists := s.Attr("name"); exists && name != "" {
			key = strings.ToLower(name)
		} else if prop, exists := s.Attr("property"); exists && prop != "" {
			key = strings.ToLower(prop)
		}
		if key != "" {
			if content, exists := s.Attr("content"); exists {
				metas[key] = content
			}
		}
	})
	return metas
}

// ExtractCookies extracts all cookies from Set-Cookie headers across the entire chain.
func ExtractCookies(chain []models.ChainedResponse) map[string]string {
	cookies := make(map[string]string)
	for _, hop := range chain {
		for _, setCookie := range hop.RawHeaders.Values("Set-Cookie") {
			// Parse "name=value; ..." — take the first part before ";"
			parts := strings.SplitN(setCookie, ";", 2)
			if len(parts) == 0 {
				continue
			}
			kv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
			if len(kv) == 2 {
				cookies[kv[0]] = kv[1]
			}
		}
	}
	return cookies
}
