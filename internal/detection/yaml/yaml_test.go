package yaml

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/PuerkitoBio/goquery"
)

// mockNavigator implements models.BrowserNavigator for testing path checks.
type mockNavigator struct {
	client    *http.Client
	jsResults map[string]string // simulated JS eval results for NavigateCaptureAndEval
}

func (m *mockNavigator) NavigateAndCapture(_ context.Context, url string) (*models.ChainedResponse, error) {
	resp, err := m.client.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return &models.ChainedResponse{
		URL:        url,
		StatusCode: resp.StatusCode,
		Body:       body,
		RawHeaders: resp.Header,
	}, nil
}

func (m *mockNavigator) NavigateCaptureAndEval(_ context.Context, url string, jsExprs []string) (*models.ChainedResponse, map[string]string, error) {
	resp, err := m.client.Get(url) //nolint:noctx
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	jsResults := make(map[string]string)
	if m.jsResults != nil {
		for _, expr := range jsExprs {
			if val, ok := m.jsResults[expr]; ok {
				jsResults[expr] = val
			}
		}
	}

	return &models.ChainedResponse{
		URL:        url,
		StatusCode: resp.StatusCode,
		Body:       body,
		RawHeaders: resp.Header,
	}, jsResults, nil
}

// --- Loader tests ---

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "wordpress.yml", `
name: WordPress
category: CMS
checks:
  headers:
    x-powered-by:
      pattern: "WordPress"
`)
	writeYAML(t, dir, "nginx.yml", `
name: Nginx
category: Server
checks:
  headers:
    server:
      pattern: "nginx"
`)
	// Non-YAML file should be ignored
	_ = os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0644)

	defs, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir failed: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
}

func TestLoadDirValidationError(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "bad.yml", `
category: CMS
checks: {}
`)

	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("expected validation error for missing name")
	}
}

func TestLoadDirInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "bad.yml"), []byte("{{{{not yaml"), 0644)

	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// --- Header check tests ---

func TestCheckHeaders(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "Nginx",
		Category: "Server",
		Checks: Checks{
			Headers: map[string]HeaderCheck{
				"server": {Pattern: "nginx"},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"Server": {"nginx/1.18.0"}}},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection")
	}
}

func TestCheckHeadersWithVersion(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "PHP",
		Category: "Language",
		Checks: Checks{
			Headers: map[string]HeaderCheck{
				"x-powered-by": {Pattern: "PHP", Version: `PHP/(\d+\.\d+\.\d+)`},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"X-Powered-By": {"PHP/8.2.3"}}},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection")
	}
	if res.Version != "8.2.3" {
		t.Errorf("expected version '8.2.3', got %q", res.Version)
	}
}

func TestCheckHeadersNoMatch(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "Nginx",
		Category: "Server",
		Checks: Checks{
			Headers: map[string]HeaderCheck{
				"server": {Pattern: "nginx"},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{"Server": {"Apache/2.4"}}},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection")
	}
}

// --- Body check tests ---

func TestCheckBody(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "WordPress",
		Category: "CMS",
		Checks: Checks{
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "wp-content/"},
				{Pattern: "wp-includes/"},
			}},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<link rel="stylesheet" href="/wp-content/themes/style.css"><script src="/wp-includes/js/jquery.js"></script>`)},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection")
	}
}

func TestCheckBodyPartialMatch(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "WordPress",
		Category: "CMS",
		Checks: Checks{
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "wp-content/"},
				{Pattern: "wp-includes/"},
				{Pattern: "wp-json/"},
			}},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<link href="/wp-content/themes/style.css">`)},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection")
	}
	// 1 out of 3 checks matched
}

func TestCheckBodyMatcherAll(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "Selligent",
		Category: "CRM",
		Checks: Checks{
			Body: BodyChecks{
				Matcher: "all",
				Patterns: []BodyCheck{
					{Pattern: "<title>Selligent Login</title>"},
					{Pattern: `<span class="release">`},
				},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<title>Selligent Login</title><span class="release">version 10.3.6</span>`)},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection when all body patterns match")
	}
}

func TestCheckBodyMatcherAllPartialFails(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "Selligent",
		Category: "CRM",
		Checks: Checks{
			Body: BodyChecks{
				Matcher: "all",
				Patterns: []BodyCheck{
					{Pattern: "<title>Selligent Login</title>"},
					{Pattern: `<span class="release">`},
				},
			},
		},
	})

	// Only one pattern matches
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{Body: []byte(`<span class="release">version 10.3.6</span>`)},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection when only one pattern matches with matcher: all")
	}
}

func TestCheckBodyMatcherAllParsedFromYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "test.yml", `
name: TestTech
category: CRM
checks:
  body:
    matcher: all
    patterns:
      - pattern: 'pattern-one'
      - pattern: 'pattern-two'
`)

	defs, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir failed: %v", err)
	}
	if defs[0].Checks.Body.Matcher != "all" {
		t.Errorf("expected matcher 'all', got %q", defs[0].Checks.Body.Matcher)
	}
	if len(defs[0].Checks.Body.Patterns) != 2 {
		t.Errorf("expected 2 body patterns, got %d", len(defs[0].Checks.Body.Patterns))
	}
}

// --- Meta check tests ---

func TestCheckMeta(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "WordPress",
		Category: "CMS",
		Checks: Checks{
			Meta: map[string]MetaCheck{
				"generator": {Pattern: "WordPress", Version: `WordPress\s+(\d+\.\d+\.?\d*)`},
			},
		},
	})

	doc, _ := goquery.NewDocumentFromReader(
		stringReader(`<html><head><meta name="generator" content="WordPress 6.3.1"></head></html>`),
	)

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{},
		Document:  doc,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection")
	}
	if res.Version != "6.3.1" {
		t.Errorf("expected version '6.3.1', got %q", res.Version)
	}
}

// --- Cookie check tests ---

func TestCheckCookies(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "Laravel",
		Category: "Framework",
		Checks: Checks{
			Cookies: map[string]CookieCheck{
				"laravel_session": {Pattern: "."},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"Set-Cookie": {"laravel_session=abc123xyz; Path=/; HttpOnly"},
				},
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection")
	}
}

func TestCheckCookiesNoMatch(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "Laravel",
		Category: "Framework",
		Checks: Checks{
			Cookies: map[string]CookieCheck{
				"laravel_session": {Pattern: "."},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{}},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection")
	}
}

func TestCheckCookiesNoPattern(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "PHP",
		Category: "Language",
		Checks: Checks{
			Cookies: map[string]CookieCheck{
				"PHPSESSID": {},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"Set-Cookie": {"PHPSESSID=abc123; Path=/"},
				},
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection when cookie exists (no pattern)")
	}
}

// --- Path check tests ---

func TestCheckPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wp-login.php" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<form id="loginform">`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "WordPress",
		Category: "CMS",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/wp-login.php", Status: 200},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "loginform"},
			}},
		},
	})

	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection: body check should match against path response")
	}
}

func TestCheckPathsNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`Not found`))
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "WordPress",
		Category: "CMS",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/wp-login.php", Status: 200},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "loginform"},
			}},
		},
	})

	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection")
	}
}

// --- Path response feeds other checks ---

func TestCheckPathResponseFeedsBodyCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/docs" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<div class="swagger-ui">API docs</div>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "Swagger UI",
		Category: "JS Library",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/api/docs", Status: 200},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "swagger-ui"},
			}},
		},
	})

	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection: body check should match against path response")
	}
	if res.Proof == nil || len(res.Proof.Body) != 1 || res.Proof.Body[0] != "swagger-ui" {
		t.Errorf("expected proof with body [swagger-ui], got %+v", res.Proof)
	}
}

func TestCheckPathResponseNoBodyMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/docs" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<div>Some other content</div>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "Swagger UI",
		Category: "JS Library",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/api/docs", Status: 200},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "swagger-ui"},
			}},
		},
	})

	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection: body check should not match path response")
	}
}

func TestCheckPathResponseFeedsJSCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/docs" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<div class="swagger-ui">API docs</div>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "Swagger UI",
		Category: "JS Library",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/api/docs", Status: 200, Browser: true},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "swagger-ui"},
			}},
			JS: []JSCheck{
				{Expression: "versions['swaggerUI']['version']", Version: true},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{},
		BrowserPool: &mockNavigator{
			client:    srv.Client(),
			jsResults: map[string]string{"versions['swaggerUI']['version']": "5.32.0"},
		},
		BaseURL: srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection")
	}
	if res.Version != "5.32.0" {
		t.Errorf("expected version '5.32.0', got %q", res.Version)
	}
}

// --- Path HTTP vs Browser mode tests ---

func TestCheckPathHTTPMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "StatusAPI",
		Category: "Other",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/status", Status: 200}, // browser: false (default)
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: `"status":"ok"`},
			}},
		},
	})

	// Only HTTPClient set, no BrowserPool — HTTP path must work
	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection via HTTP path check")
	}
}

func TestCheckPathHTTPModeNilClient(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "StatusAPI",
		Category: "Other",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/status", Status: 200},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: `"status":"ok"`},
			}},
		},
	})

	// No HTTPClient and no BrowserPool
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{},
		BaseURL:   "http://localhost:9999",
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection when HTTP client is nil")
	}
}

func TestCheckPathBrowserModeExplicit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<div id="app">SPA</div>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "SPA",
		Category: "JS Framework",
		Checks: Checks{
			Paths: []PathCheck{
				{Path: "/app", Status: 200, Browser: true},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: `id="app"`},
			}},
		},
	})

	// BrowserPool set, no HTTPClient — browser path must work
	ctx := &models.DetectionContext{
		Responses:   []models.ChainedResponse{},
		BrowserPool: &mockNavigator{client: srv.Client()},
		BaseURL:     srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection via browser path check")
	}
}

func TestPathCheckBrowserFieldParsedFromYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "test.yml", `
name: TestTech
category: CMS
checks:
  paths:
    - path: /api
      status: 200
    - path: /app
      status: 200
      browser: true
`)

	defs, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir failed: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	paths := defs[0].Checks.Paths
	if len(paths) != 2 {
		t.Fatalf("expected 2 path checks, got %d", len(paths))
	}
	if paths[0].Browser {
		t.Error("first path check should have browser=false by default")
	}
	if !paths[1].Browser {
		t.Error("second path check should have browser=true")
	}
}

// --- JS check tests ---

func TestCheckJSSkippedWithoutBrowser(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "jQuery",
		Category: "JS Library",
		Checks: Checks{
			JS: []JSCheck{
				{Expression: "jQuery.fn.jquery", Version: true},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	// No checks counted (JS skipped), so not detected
	if res.Detected {
		t.Error("expected no detection when browser is nil")
	}
}

// --- Combined check tests ---

func TestCombinedChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wp-login.php" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "WordPress",
		Category: "CMS",
		Checks: Checks{
			Headers: map[string]HeaderCheck{
				"x-powered-by": {Pattern: "WordPress"},
			},
			Body: BodyChecks{Patterns: []BodyCheck{
				{Pattern: "wp-content/"},
			}},
			Meta: map[string]MetaCheck{
				"generator": {Pattern: "WordPress", Version: `WordPress\s+(\d+\.\d+)`},
			},
			Paths: []PathCheck{
				{Path: "/wp-login.php", Status: 200},
			},
		},
	})

	doc, _ := goquery.NewDocumentFromReader(
		stringReader(`<html><head><meta name="generator" content="WordPress 6.3"></head></html>`),
	)

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{"X-Powered-By": {"WordPress"}},
				Body:       []byte(`<link href="/wp-content/themes/style.css">`),
			},
		},
		Document:   doc,
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected detection")
	}
	if res.Version != "6.3" {
		t.Errorf("expected version '6.3', got %q", res.Version)
	}
}

// --- Multi-response chain tests ---

func TestCheckAcrossChain(t *testing.T) {
	det := NewDetector(Definition{
		Name:     "Cloudflare",
		Category: "CDN",
		Checks: Checks{
			Headers: map[string]HeaderCheck{
				"cf-ray": {Pattern: "."},
			},
		},
	})

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{RawHeaders: http.Header{}},
			{RawHeaders: http.Header{"Cf-Ray": {"abc123"}}},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection from second response in chain")
	}
}

// --- Favicon hash check tests ---

func TestCheckFaviconHash(t *testing.T) {
	faviconData := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/x-icon")
			_, _ = w.Write(faviconData)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	// Compute the expected hash
	expectedHash, ok := faviconMMH3(srv.Client(), srv.URL, nil)
	if !ok {
		t.Fatal("failed to compute expected hash")
	}

	det := NewDetector(Definition{
		Name:     "TestApp",
		Category: "CMS",
		Checks: Checks{
			FaviconHash: []int32{expectedHash},
		},
	})

	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Error("expected detection via favicon hash")
	}
}

func TestCheckFaviconHashNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write([]byte("some random favicon"))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := NewDetector(Definition{
		Name:     "TestApp",
		Category: "CMS",
		Checks: Checks{
			FaviconHash: []int32{999999999}, // wrong hash
		},
	})

	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection with wrong favicon hash")
	}
}

// --- Helpers ---

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func stringReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

// Import strings for stringReader
var _ = fmt.Sprint // keep fmt imported
