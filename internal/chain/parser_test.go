package chain

import (
	"net/http"
	"testing"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

const fixtureHTML = `<!DOCTYPE html>
<html>
<head>
	<title>Test Page</title>
	<meta name="generator" content="WordPress 6.3.1">
	<meta name="description" content="A test website">
	<meta property="og:title" content="OG Title">
	<meta property="og:type" content="website">
	<meta charset="utf-8">
</head>
<body>
	<h1>Hello World</h1>
	<script src="/wp-includes/js/jquery.min.js"></script>
</body>
</html>`

func TestParseDocument(t *testing.T) {
	resp := &models.ChainedResponse{
		Body: []byte(fixtureHTML),
	}

	doc, err := ParseDocument(resp)
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	title := doc.Find("title").Text()
	if title != "Test Page" {
		t.Errorf("expected title 'Test Page', got %q", title)
	}

	scripts := doc.Find("script")
	if scripts.Length() != 1 {
		t.Errorf("expected 1 script tag, got %d", scripts.Length())
	}
}

func TestExtractMeta(t *testing.T) {
	resp := &models.ChainedResponse{
		Body: []byte(fixtureHTML),
	}

	doc, err := ParseDocument(resp)
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	metas := ExtractMeta(doc)

	tests := []struct {
		key  string
		want string
	}{
		{"generator", "WordPress 6.3.1"},
		{"description", "A test website"},
		{"og:title", "OG Title"},
		{"og:type", "website"},
	}

	for _, tt := range tests {
		got, ok := metas[tt.key]
		if !ok {
			t.Errorf("meta %q not found", tt.key)
			continue
		}
		if got != tt.want {
			t.Errorf("meta %q: got %q, want %q", tt.key, got, tt.want)
		}
	}

	// charset meta has no content attribute, should not appear
	if _, ok := metas["charset"]; ok {
		t.Error("charset meta should not be extracted (no content attribute)")
	}
}

func TestExtractMetaEmpty(t *testing.T) {
	resp := &models.ChainedResponse{
		Body: []byte("<html><head></head><body></body></html>"),
	}

	doc, err := ParseDocument(resp)
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	metas := ExtractMeta(doc)
	if len(metas) != 0 {
		t.Errorf("expected empty metas, got %v", metas)
	}
}

func TestExtractCookies(t *testing.T) {
	chain := []models.ChainedResponse{
		{
			URL:        "https://example.com",
			StatusCode: 301,
			RawHeaders: http.Header{
				"Set-Cookie": {
					"session_id=abc123; Path=/; HttpOnly",
					"tracking=xyz; Path=/; Secure",
				},
			},
		},
		{
			URL:        "https://www.example.com",
			StatusCode: 200,
			RawHeaders: http.Header{
				"Set-Cookie": {
					"wordpress_logged_in=user1; Path=/wp-admin",
				},
			},
		},
	}

	cookies := ExtractCookies(chain)

	expected := map[string]string{
		"session_id":          "abc123",
		"tracking":            "xyz",
		"wordpress_logged_in": "user1",
	}

	for k, want := range expected {
		got, ok := cookies[k]
		if !ok {
			t.Errorf("cookie %q not found", k)
			continue
		}
		if got != want {
			t.Errorf("cookie %q: got %q, want %q", k, got, want)
		}
	}

	if len(cookies) != len(expected) {
		t.Errorf("expected %d cookies, got %d", len(expected), len(cookies))
	}
}

func TestExtractCookiesNone(t *testing.T) {
	chain := []models.ChainedResponse{
		{
			URL:        "https://example.com",
			StatusCode: 200,
			RawHeaders: http.Header{},
		},
	}

	cookies := ExtractCookies(chain)
	if len(cookies) != 0 {
		t.Errorf("expected no cookies, got %v", cookies)
	}
}

func TestExtractCookiesOverwrite(t *testing.T) {
	chain := []models.ChainedResponse{
		{
			URL:        "https://example.com",
			StatusCode: 301,
			RawHeaders: http.Header{
				"Set-Cookie": {"token=old; Path=/"},
			},
		},
		{
			URL:        "https://www.example.com",
			StatusCode: 200,
			RawHeaders: http.Header{
				"Set-Cookie": {"token=new; Path=/"},
			},
		},
	}

	cookies := ExtractCookies(chain)
	if cookies["token"] != "new" {
		t.Errorf("expected cookie to be overwritten to 'new', got %q", cookies["token"])
	}
}
