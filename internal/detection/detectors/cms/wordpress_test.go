package cms

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

func TestWordPressHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		version string
	}{
		{
			name:    "x-wordpress-version",
			headers: http.Header{"X-Wordpress-Version": {"6.4.2"}},
			version: "6.4.2",
		},
		{
			name:    "link api.w.org",
			headers: http.Header{"Link": {`<https://example.com/wp-json/>; rel="https://api.w.org/"`}},
		},
		{
			name:    "x-pingback",
			headers: http.Header{"X-Pingback": {"https://example.com/xmlrpc.php"}},
		},
		{
			name:    "x-powered-by WordPress",
			headers: http.Header{"X-Powered-By": {"WordPress"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			det := &WordPressDetector{}
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
				t.Fatal("expected WordPress detected via headers")
			}
			if tt.version != "" && res.Version != tt.version {
				t.Errorf("expected version %q, got %q", tt.version, res.Version)
			}
		})
	}
}

func TestWordPressBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "wp-content link",
			body: `<link rel="stylesheet" href="/wp-content/themes/style.css">`,
		},
		{
			name: "wp-includes script",
			body: `<script src="/wp-includes/js/jquery.js"></script>`,
		},
		{
			name: "wp-embed script",
			body: `<script src="/wp-includes/js/wp-embed.min.js"></script>`,
		},
		{
			name: "gutenberg block",
			body: `<div class="wp-block-paragraph">Hello</div>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			det := &WordPressDetector{}
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
				t.Fatalf("expected WordPress detected via body: %s", tt.name)
			}
		})
	}
}

func TestWordPressBodyEmbedVersion(t *testing.T) {
	det := &WordPressDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<script src="/wp-includes/js/wp-embed.min.js?ver=6.4.2"></script>`)},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected")
	}
	if res.Version != "6.4.2" {
		t.Errorf("expected version 6.4.2, got %q", res.Version)
	}
}

func TestWordPressBodyEmojiVersion(t *testing.T) {
	det := &WordPressDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<script>window._wpemojiSettings={"baseUrl":"https:\/\/s.w.org\/images\/core\/emoji\/15.0.3\/72x72\/","ext":".png","svgUrl":"https:\/\/s.w.org\/images\/core\/emoji\/15.0.3\/svg\/","svgExt":".svg","source":{"concatemoji":"https:\/\/example.com\/wp-includes\/js\/wp-emoji-release.min.js?ver=6.5.3"}};</script>`)},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected via emoji script")
	}
	if res.Version != "6.5.3" {
		t.Errorf("expected version 6.5.3, got %q", res.Version)
	}
}

func TestWordPressMeta(t *testing.T) {
	det := &WordPressDetector{}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><meta name="generator" content="WordPress 6.5.0"></head></html>`,
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
		t.Fatal("expected WordPress detected via meta generator")
	}
	if res.Version != "6.5.0" {
		t.Errorf("expected version 6.5.0, got %q", res.Version)
	}
}

func TestWordPressCookies(t *testing.T) {
	det := &WordPressDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"Set-Cookie": {"wp-settings-1=foo; path=/"},
				},
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected via cookies")
	}
}

func TestWordPressJS(t *testing.T) {
	det := &WordPressDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{{}},
		JSResults: map[string]string{
			`typeof wp !== 'undefined' && typeof wp.ajax !== 'undefined'`: "true",
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected via JS")
	}
}

func TestWordPressNotDetected(t *testing.T) {
	det := &WordPressDetector{}
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
		t.Error("expected no WordPress detection")
	}
}

// mockBrowserNavigator implements BrowserNavigator for testing path probes.
type mockBrowserNavigator struct {
	responses map[string]*models.ChainedResponse
}

func (m *mockBrowserNavigator) NavigateAndCapture(_ context.Context, url string) (*models.ChainedResponse, error) {
	if resp, ok := m.responses[url]; ok {
		return resp, nil
	}
	return &models.ChainedResponse{StatusCode: 404}, nil
}

func (m *mockBrowserNavigator) NavigateCaptureAndEval(_ context.Context, url string, _ []string) (*models.ChainedResponse, map[string]string, error) {
	resp, err := m.NavigateAndCapture(context.Background(), url)
	return resp, nil, err
}

func TestWordPressPathProbeFeed(t *testing.T) {
	det := &WordPressDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{
		responses: map[string]*models.ChainedResponse{
			base + "/?feed=atom": {
				StatusCode: 200,
				Body:       []byte(`<generator uri="https://wordpress.org/" version="6.3.1">WordPress</generator>`),
			},
		},
	}

	// Detected via body but no version → should trigger probes
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<link rel="stylesheet" href="/wp-content/themes/style.css">`)},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected")
	}
	if res.Version != "6.3.1" {
		t.Errorf("expected version 6.3.1, got %q", res.Version)
	}
}

func TestWordPressPathProbeOPML(t *testing.T) {
	det := &WordPressDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{
		responses: map[string]*models.ChainedResponse{
			base + "/?feed=atom": {StatusCode: 404},
			base + "/wp-links-opml.php": {
				StatusCode: 200,
				Body:       []byte(`<!-- generator="WordPress/6.2.0" -->`),
			},
		},
	}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<link rel="stylesheet" href="/wp-content/themes/style.css">`)},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected")
	}
	if res.Version != "6.2.0" {
		t.Errorf("expected version 6.2.0, got %q", res.Version)
	}
}

func TestWordPressPathProbeReadme(t *testing.T) {
	det := &WordPressDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{
		responses: map[string]*models.ChainedResponse{
			base + "/?feed=atom":        {StatusCode: 404},
			base + "/wp-links-opml.php": {StatusCode: 404},
			base + "/readme.html": {
				StatusCode: 200,
				Body:       []byte(`<h1 id="logo">WordPress</h1><br /> Version 6.7`),
			},
		},
	}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<link rel="stylesheet" href="/wp-content/themes/style.css">`)},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected")
	}
	if res.Version != "6.7" {
		t.Errorf("expected version 6.7, got %q", res.Version)
	}
}

func TestWordPressPathProbeLoginAssets(t *testing.T) {
	det := &WordPressDetector{}
	base := "https://example.com"

	nav := &mockBrowserNavigator{
		responses: map[string]*models.ChainedResponse{
			base + "/?feed=atom":        {StatusCode: 404},
			base + "/wp-links-opml.php": {StatusCode: 404},
			base + "/readme.html":       {StatusCode: 404},
			base + "/wp-login.php": {
				StatusCode: 200,
				Body:       []byte(`<link rel='stylesheet' id='dashicons-css' href='https://example.com/wp-includes/css/dashicons.min.css?ver=6.4.2' media='all' />`),
			},
		},
	}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<link rel="stylesheet" href="/wp-content/themes/style.css">`)},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected")
	}
	if res.Version != "6.4.2" {
		t.Errorf("expected version 6.4.2, got %q", res.Version)
	}
}

func TestWordPressNoProbeWhenVersionKnown(t *testing.T) {
	det := &WordPressDetector{}
	base := "https://example.com"

	// Navigator that would fail if called — probes should be skipped
	nav := &mockBrowserNavigator{responses: map[string]*models.ChainedResponse{}}

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{"X-Wordpress-Version": {"6.4.2"}},
			},
		},
		BrowserPool: nav,
		BaseURL:     base,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected")
	}
	if res.Version != "6.4.2" {
		t.Errorf("expected version 6.4.2, got %q", res.Version)
	}
}

func TestWordPressJSExpressions(t *testing.T) {
	det := &WordPressDetector{}
	exprs := det.JSExpressions()
	if len(exprs) != 3 {
		t.Errorf("expected 3 JS expressions, got %d", len(exprs))
	}
}

func TestWordPressProof(t *testing.T) {
	det := &WordPressDetector{}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><meta name="generator" content="WordPress 6.5.0"></head></html>`,
	))

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"Link": {`<https://example.com/wp-json/>; rel="https://api.w.org/"`},
				},
				Body: []byte(`<link rel="stylesheet" href="/wp-content/themes/style.css">`),
			},
		},
		Document: doc,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected WordPress detected")
	}
	if res.Proof == nil {
		t.Fatal("expected proof to be set")
	}
	if !containsStr(res.Proof.Headers, "link") {
		t.Errorf("proof missing header link, got: %v", res.Proof.Headers)
	}
	if len(res.Proof.Body) == 0 {
		t.Errorf("proof missing body evidence, got: %v", res.Proof.Body)
	}
	if !containsStr(res.Proof.Meta, "generator") {
		t.Errorf("proof missing meta generator, got: %v", res.Proof.Meta)
	}
}

func containsStr(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
