package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JoshuaMart/fingerprinter/internal/config"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

func browserControlURL() string {
	if v := os.Getenv("FINGERPRINTER_BROWSER_CONTROL_URL"); v != "" {
		return v
	}
	return "http://localhost:9222"
}

func testConfig(t *testing.T, yamlDir string) *config.Config {
	t.Helper()
	return &config.Config{
		Server: config.ServerConfig{
			Port:        3001,
			ReadTimeout: 10 * time.Second,
		},
		Scanner: config.ScannerConfig{
			MaxRedirects:    10,
			RequestTimeout:  5 * time.Second,
			Headers:         map[string]string{"User-Agent": "TestAgent/1.0"},
			ConcurrentScans: 5,
		},
		Browser: config.BrowserConfig{
			MaxPages:    10,
			PageTimeout: 10 * time.Second,
			ControlURLs: []string{browserControlURL()},
		},
		Detections: config.DetectionsConfig{
			YAMLDir: yamlDir,
		},
	}
}

func newScanner(t *testing.T, cfg *config.Config) *Scanner {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Skipf("browser not available, skipping: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func writeDetection(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestScanBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Powered-By", "PHP/8.2.3")
		w.Header().Set("Set-Cookie", "PHPSESSID=abc123; Path=/")
		_, _ = fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>Test Site</title></head>
<body><h1>Hello</h1></body></html>`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeDetection(t, dir, "php.yml", `
name: PHP
category: Language
checks:
  headers:
    x-powered-by:
      pattern: "PHP"
      version: "(\\d+\\.\\d+\\.\\d+)"
  cookies:
    PHPSESSID:
`)

	cfg := testConfig(t, dir)
	s := newScanner(t, cfg)

	result, err := s.Scan(context.Background(), models.ScanRequest{
		URL:     srv.URL,
		Options: &models.ScanOptions{TimeoutSeconds: 10},
	})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if result.URL != srv.URL {
		t.Errorf("expected URL %s, got %s", srv.URL, result.URL)
	}
	if len(result.Chain) < 1 {
		t.Errorf("expected at least 1 hop, got %d", len(result.Chain))
	}

	// Check PHP detected
	found := false
	for _, tech := range result.Technologies {
		if tech.Name == "PHP" {
			found = true
			if tech.Version != "8.2.3" {
				t.Errorf("expected PHP version 8.2.3, got %q", tech.Version)
			}
		}
	}
	if !found {
		t.Error("expected PHP in technologies")
	}

	// Check metadata
	if result.Metadata == nil {
		t.Error("expected metadata to be present")
	}
}

func TestScanWithRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><head><title>Final</title></head><body></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := testConfig(t, t.TempDir())
	s := newScanner(t, cfg)

	result, err := s.Scan(context.Background(), models.ScanRequest{
		URL:     srv.URL,
		Options: &models.ScanOptions{TimeoutSeconds: 10},
	})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(result.Chain) < 2 {
		t.Fatalf("expected at least 2 hops, got %d", len(result.Chain))
	}
}

func TestScanTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	cfg := testConfig(t, t.TempDir())
	s := newScanner(t, cfg)

	_, err := s.Scan(context.Background(), models.ScanRequest{
		URL:     srv.URL,
		Options: &models.ScanOptions{TimeoutSeconds: 1},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestScanConcurrencyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html></html>")
	}))
	defer srv.Close()

	cfg := testConfig(t, t.TempDir())
	cfg.Scanner.ConcurrentScans = 2
	s := newScanner(t, cfg)

	// Launch 4 scans, only 2 should run concurrently
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, scanErr := s.Scan(context.Background(), models.ScanRequest{
				URL:     srv.URL,
				Options: &models.ScanOptions{TimeoutSeconds: 10},
			})
			if scanErr != nil {
				t.Errorf("scan error: %v", scanErr)
			}
		}()
	}
	wg.Wait()
}

func TestScanNoDetections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body>Plain site</body></html>")
	}))
	defer srv.Close()

	cfg := testConfig(t, t.TempDir())
	s := newScanner(t, cfg)

	result, err := s.Scan(context.Background(), models.ScanRequest{
		URL:     srv.URL,
		Options: &models.ScanOptions{TimeoutSeconds: 10},
	})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(result.Technologies) != 0 {
		t.Errorf("expected 0 technologies, got %d", len(result.Technologies))
	}
}
