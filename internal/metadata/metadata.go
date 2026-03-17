package metadata

import (
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

var sitemapRe = regexp.MustCompile(`(?i)Sitemap:\s*(https?://\S+)`)

// Fetch retrieves robots.txt, sitemap and favicon metadata for the given base URL.
func Fetch(client *http.Client, baseURL string, doc *goquery.Document) *models.ScanMetadata {
	base := strings.TrimRight(baseURL, "/")
	meta := &models.ScanMetadata{}

	robotsBody := fetchBody(client, base+"/robots.txt")
	if robotsBody != "" {
		meta.RobotsTXT = true
		// Try to extract sitemap from robots.txt
		if m := sitemapRe.FindStringSubmatch(robotsBody); len(m) > 1 {
			meta.Sitemap = &m[1]
		}
	}

	// Check for llms.txt
	if exists(client, base+"/llms.txt") {
		meta.LLMsTXT = true
	}

	// If no sitemap found in robots.txt, check /sitemap.xml directly
	if meta.Sitemap == nil {
		sitemapURL := base + "/sitemap.xml"
		if exists(client, sitemapURL) {
			meta.Sitemap = &sitemapURL
		}
	}

	// Try to extract favicon from HTML first
	if doc != nil {
		if href := extractFaviconFromHTML(doc, base); href != "" {
			meta.Favicon = &href
		}
	}

	// Fallback to /favicon.ico
	if meta.Favicon == nil {
		faviconURL := base + "/favicon.ico"
		if exists(client, faviconURL) {
			meta.Favicon = &faviconURL
		}
	}

	return meta
}

func fetchBody(client *http.Client, url string) string {
	if client == nil {
		return ""
	}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

func exists(client *http.Client, url string) bool {
	if client == nil {
		return false
	}
	resp, err := client.Head(url) //nolint:noctx
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func extractFaviconFromHTML(doc *goquery.Document, baseURL string) string {
	var href string
	doc.Find(`link[rel="icon"], link[rel="shortcut icon"]`).First().Each(func(_ int, s *goquery.Selection) {
		if h, exists := s.Attr("href"); exists && h != "" {
			href = h
		}
	})

	if href == "" {
		return ""
	}

	// Resolve relative URLs
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
