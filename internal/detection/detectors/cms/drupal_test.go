package cms

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

func TestDrupalHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		version string
	}{
		{
			name:    "x-generator with version",
			headers: http.Header{"X-Generator": {"Drupal 10 (https://www.drupal.org)"}},
			version: "10",
		},
		{
			name:    "x-drupal-cache",
			headers: http.Header{"X-Drupal-Cache": {"HIT"}},
		},
		{
			name:    "x-drupal-dynamic-cache",
			headers: http.Header{"X-Drupal-Dynamic-Cache": {"MISS"}},
		},
		{
			name:    "x-drupal-route-normalizer",
			headers: http.Header{"X-Drupal-Route-Normalizer": {"1"}},
		},
		{
			name:    "expires 19 Nov 1978",
			headers: http.Header{"Expires": {"Sun, 19 Nov 1978 05:00:00 GMT"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			det := &DrupalDetector{}
			ctx := &models.DetectionContext{
				Responses: []models.ChainedResponse{
					{RawHeaders: tt.headers},
				},
			}

			res, err := det.Detect(ctx)
			if err != nil {
				t.Fatalf("Detect failed: %v", err)
			}
			if !res.Detected {
				t.Fatal("expected Drupal detected via headers")
			}
			if tt.version != "" && res.Version != tt.version {
				t.Errorf("expected version %q, got %q", tt.version, res.Version)
			}
		})
	}
}

func TestDrupalBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "sites/default/themes link",
			body: `<link href="/sites/default/themes/bartik/style.css" rel="stylesheet">`,
		},
		{
			name: "sites/all/modules style",
			body: `<style href="/sites/all/modules/views/css/views.css">`,
		},
		{
			name: "data-drupal-link-system-path",
			body: `<a href="/" data-drupal-link-system-path="<front>">Home</a>`,
		},
		{
			name: "data-drupal-selector",
			body: `<div data-drupal-selector="edit-name">Name</div>`,
		},
		{
			name: "drupal.js",
			body: `<script src="/misc/drupal.js"></script>`,
		},
		{
			name: "drupal.init.js",
			body: `<script src="/misc/drupal.init.js"></script>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			det := &DrupalDetector{}
			ctx := &models.DetectionContext{
				Responses: []models.ChainedResponse{
					{Body: []byte(tt.body)},
				},
			}

			res, err := det.Detect(ctx)
			if err != nil {
				t.Fatalf("Detect failed: %v", err)
			}
			if !res.Detected {
				t.Fatalf("expected Drupal detected via body: %s", tt.name)
			}
		})
	}
}

func TestDrupalBodyVersion(t *testing.T) {
	det := &DrupalDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<script src="/core/misc/drupal.js?v=10.2.3"></script>`)},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected")
	}
	if res.Version != "10.2.3" {
		t.Errorf("expected version 10.2.3, got %q", res.Version)
	}
}

func TestDrupalMeta(t *testing.T) {
	det := &DrupalDetector{}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><meta name="generator" content="Drupal 10 (https://www.drupal.org)"></head></html>`,
	))

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{{}},
		Document:  doc,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected via meta generator")
	}
	if res.Version != "10" {
		t.Errorf("expected version 10, got %q", res.Version)
	}
}

func TestDrupalCookies(t *testing.T) {
	det := &DrupalDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"Set-Cookie": {"SESSabc123def456abc123def456abc123de=xyz; path=/"},
				},
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected via cookies")
	}
}

func TestDrupalCookieNoMatch(t *testing.T) {
	det := &DrupalDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"Set-Cookie": {"SESSION=abc; path=/"},
				},
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no Drupal detection for non-matching cookie")
	}
}

func TestDrupalJS(t *testing.T) {
	det := &DrupalDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{{}},
		JSResults: map[string]string{
			`typeof Drupal !== 'undefined' && typeof Drupal.behaviors !== 'undefined'`: "true",
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected via JS")
	}
}

func TestDrupalNotDetected(t *testing.T) {
	det := &DrupalDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{"Server": {"nginx"}},
				Body:       []byte("<html><body>Hello</body></html>"),
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no Drupal detection")
	}
}

func TestDrupalPathProbeChangelog(t *testing.T) {
	det := &DrupalDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{
		responses: map[string]*models.ChainedResponse{
			base + "/CHANGELOG.txt": {
				StatusCode: 200,
				Body:       []byte("Drupal 7.98\n\nChanges since 7.97:"),
			},
		},
	}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"X-Drupal-Cache": {"HIT"}}},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected")
	}
	if res.Version != "7.98" {
		t.Errorf("expected version 7.98, got %q", res.Version)
	}
}

func TestDrupalPathProbeCoreInstall(t *testing.T) {
	det := &DrupalDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{
		responses: map[string]*models.ChainedResponse{
			base + "/CHANGELOG.txt": {StatusCode: 403},
			base + "/core/install.php": {
				StatusCode: 200,
				Body:       []byte(`<script src="/core/misc/install.js?v=10.2.3"></script>`),
			},
		},
	}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"X-Drupal-Cache": {"HIT"}}},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected")
	}
	if res.Version != "10.2.3" {
		t.Errorf("expected version 10.2.3, got %q", res.Version)
	}
}

func TestDrupalPathProbeCoreChangelog(t *testing.T) {
	det := &DrupalDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{
		responses: map[string]*models.ChainedResponse{
			base + "/CHANGELOG.txt":    {StatusCode: 403},
			base + "/core/install.php": {StatusCode: 403},
			base + "/core/CHANGELOG.txt": {
				StatusCode: 200,
				Body:       []byte("Drupal 10\n\nSome changes here"),
			},
		},
	}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"X-Drupal-Cache": {"HIT"}}},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected")
	}
	if res.Version != "10" {
		t.Errorf("expected version 10, got %q", res.Version)
	}
}

func TestDrupalNoProbeWhenVersionKnown(t *testing.T) {
	det := &DrupalDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{responses: map[string]*models.ChainedResponse{}}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"X-Generator": {"Drupal 10 (https://www.drupal.org)"}}},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected")
	}
	if res.Version != "10" {
		t.Errorf("expected version 10, got %q", res.Version)
	}
}

func TestDrupalNoProbeWhenSkipPathChecks(t *testing.T) {
	det := &DrupalDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{responses: map[string]*models.ChainedResponse{}}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"X-Drupal-Cache": {"HIT"}}},
		},
		BrowserPool:    nav,
		BaseURL:        base,
		SkipPathChecks: true,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected")
	}
	if res.Version != "" {
		t.Errorf("expected no version with skip_path_checks, got %q", res.Version)
	}
}

func TestDrupalProof(t *testing.T) {
	det := &DrupalDetector{}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><meta name="generator" content="Drupal 10 (https://www.drupal.org)"></head></html>`,
	))

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"X-Drupal-Cache": {"HIT"},
				},
				Body: []byte(`<a data-drupal-link-system-path="<front>">Home</a>`),
			},
		},
		Document: doc,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Drupal detected")
	}
	if res.Proof == nil {
		t.Fatal("expected proof to be set")
	}
	if !containsStr(res.Proof.Headers, "x-drupal-cache") {
		t.Errorf("proof missing header x-drupal-cache, got: %v", res.Proof.Headers)
	}
	if len(res.Proof.Body) == 0 {
		t.Errorf("proof missing body evidence, got: %v", res.Proof.Body)
	}
	if !containsStr(res.Proof.Meta, "generator") {
		t.Errorf("proof missing meta generator, got: %v", res.Proof.Meta)
	}
}

func TestDrupalJSExpressions(t *testing.T) {
	det := &DrupalDetector{}
	exprs := det.JSExpressions()
	if len(exprs) != 1 {
		t.Errorf("expected 1 JS expression, got %d", len(exprs))
	}
}
