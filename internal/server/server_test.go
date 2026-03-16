package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoshuaMart/fingerprinter/internal/config"
	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/JoshuaMart/fingerprinter/internal/scanner"
)

func browserControlURL() string {
	if v := os.Getenv("FINGERPRINTER_BROWSER_CONTROL_URL"); v != "" {
		return v
	}
	return "ws://localhost:9222"
}

func setupServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "php.yml"), []byte(`
name: PHP
category: Language
checks:
  headers:
    x-powered-by:
      pattern: "PHP"
      version: "(\\d+\\.\\d+\\.\\d+)"
`), 0644)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:        3001,
			ReadTimeout: 10 * time.Second,
		},
		Scanner: config.ScannerConfig{
			MaxRedirects:    10,
			RequestTimeout:  5 * time.Second,
			Headers:         map[string]string{"User-Agent": "Test/1.0"},
			ConcurrentScans: 5,
		},
		Browser: config.BrowserConfig{
			PoolSize:    1,
			PageTimeout: 10 * time.Second,
			ControlURL:  browserControlURL(),
		},
		Detections: config.DetectionsConfig{YAMLDir: dir},
	}

	scn, err := scanner.New(cfg)
	if err != nil {
		t.Skipf("browser not available, skipping: %v", err)
	}
	t.Cleanup(func() { scn.Close() })

	srv := New(cfg, scn)
	return srv, httptest.NewServer(srv.Handler())
}

func TestHealthEndpoint(t *testing.T) {
	_, ts := setupServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", body["status"])
	}
	if body["version"] == "" {
		t.Error("expected version to be present")
	}
}

func TestDetectionsEndpoint(t *testing.T) {
	_, ts := setupServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/detections")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Detections []detectionInfo `json:"detections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	// Should have PHP (yaml) + Magento (complex detector)
	if len(body.Detections) < 2 {
		t.Errorf("expected at least 2 detections, got %d", len(body.Detections))
	}

	found := false
	for _, d := range body.Detections {
		if d.Name == "PHP" && d.Category == "Language" {
			found = true
		}
	}
	if !found {
		t.Error("expected PHP detection in list")
	}
}

func TestScanEndpoint(t *testing.T) {
	// Target server that looks like PHP
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Powered-By", "PHP/8.1.0")
		_, _ = fmt.Fprint(w, "<html><head><title>Test</title></head><body></body></html>")
	}))
	defer target.Close()

	_, ts := setupServer(t)
	defer ts.Close()

	reqBody := fmt.Sprintf(`{"url":"%s","options":{"timeout_seconds":10}}`, target.URL)
	resp, err := http.Post(ts.URL+"/scan", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result models.ScanResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if result.URL != target.URL {
		t.Errorf("expected URL %s, got %s", target.URL, result.URL)
	}

	found := false
	for _, tech := range result.Technologies {
		if tech.Name == "PHP" {
			found = true
			if tech.Version != "8.1.0" {
				t.Errorf("expected PHP 8.1.0, got %q", tech.Version)
			}
		}
	}
	if !found {
		t.Error("expected PHP in scan results")
	}
}

func TestScanEndpointMissingURL(t *testing.T) {
	_, ts := setupServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/scan", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestScanEndpointInvalidJSON(t *testing.T) {
	_, ts := setupServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/scan", "application/json", strings.NewReader(`not json`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGracefulShutdown(t *testing.T) {
	srv, _ := setupServer(t)

	// Use a random free port to avoid conflicts
	srv.cfg.Server.Port = 0

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	// Give the server time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel the context (simulates SIGINT/SIGTERM)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected clean shutdown, got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5 seconds")
	}
}
