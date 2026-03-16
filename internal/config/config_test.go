package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with no file should succeed: %v", err)
	}

	if cfg.Server.Port != 3001 {
		t.Errorf("expected default port 3001, got %d", cfg.Server.Port)
	}
	if cfg.Scanner.MaxRedirects != 10 {
		t.Errorf("expected default max_redirects 10, got %d", cfg.Scanner.MaxRedirects)
	}
	if cfg.Scanner.RequestTimeout != 10*time.Second {
		t.Errorf("expected default request_timeout 10s, got %v", cfg.Scanner.RequestTimeout)
	}
	if cfg.Scanner.Headers["User-Agent"] != "Fingerprinter/1.0" {
		t.Errorf("expected default User-Agent header, got %v", cfg.Scanner.Headers)
	}
	if cfg.Scanner.ConcurrentScans != 50 {
		t.Errorf("expected default concurrent_scans 50, got %d", cfg.Scanner.ConcurrentScans)
	}
	if cfg.Browser.ControlURL != "ws://localhost:9222" {
		t.Errorf("expected default control_url 'ws://localhost:9222', got %q", cfg.Browser.ControlURL)
	}
}

func TestLoadFromYAML(t *testing.T) {
	content := []byte(`
server:
  port: 9090
  read_timeout: 60s
scanner:
  max_redirects: 5
  request_timeout: 20s
  headers:
    User-Agent: "CustomAgent/2.0"
    X-Custom: "test"
  concurrent_scans: 100
browser:
  control_url: "ws://remote:9222"
  pool_size: 10
  page_timeout: 30s
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Scanner.MaxRedirects != 5 {
		t.Errorf("expected max_redirects 5, got %d", cfg.Scanner.MaxRedirects)
	}
	if cfg.Scanner.Headers["User-Agent"] != "CustomAgent/2.0" {
		t.Errorf("expected custom User-Agent, got %s", cfg.Scanner.Headers["User-Agent"])
	}
	if cfg.Scanner.Headers["X-Custom"] != "test" {
		t.Errorf("expected X-Custom header, got %s", cfg.Scanner.Headers["X-Custom"])
	}
	if cfg.Browser.ControlURL != "ws://remote:9222" {
		t.Errorf("expected control_url 'ws://remote:9222', got %q", cfg.Browser.ControlURL)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("FINGERPRINTER_SERVER_PORT", "4000")
	t.Setenv("FINGERPRINTER_SCANNER_USER_AGENT", "EnvAgent/1.0")
	t.Setenv("FINGERPRINTER_BROWSER_CONTROL_URL", "ws://custom:9222")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 4000 {
		t.Errorf("expected port 4000 from env, got %d", cfg.Server.Port)
	}
	if cfg.Scanner.Headers["User-Agent"] != "EnvAgent/1.0" {
		t.Errorf("expected User-Agent from env, got %s", cfg.Scanner.Headers["User-Agent"])
	}
	if cfg.Browser.ControlURL != "ws://custom:9222" {
		t.Errorf("expected control_url from env, got %q", cfg.Browser.ControlURL)
	}
}

func TestValidationInvalidPort(t *testing.T) {
	content := []byte(`
server:
  port: 99999
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid port")
	}
}

func TestValidationInvalidRedirects(t *testing.T) {
	content := []byte(`
scanner:
  max_redirects: 0
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid max_redirects")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
