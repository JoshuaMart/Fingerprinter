package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func startTestServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
	<h1>Hello World</h1>
	<p>Test paragraph</p>
	<script>
		window.testVersion = "3.6.0";
		window.testFlag = true;
	</script>
</body>
</html>`)
	})
	return httptest.NewServer(mux)
}

func setupPool(t *testing.T) *Pool {
	t.Helper()
	pool, err := NewPool(2, 10*time.Second)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func TestPoolCreateAndClose(t *testing.T) {
	pool := setupPool(t)
	if pool.Browser() == nil {
		t.Fatal("expected non-nil browser")
	}
}

func TestNavigate(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	var title string
	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		el, err := result.Page.Element("h1")
		if err != nil {
			return err
		}
		title, err = el.Text()
		return err
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
	if title != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", title)
	}
}

func TestEvalJS(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	var version string
	var flagResult string
	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		var err error
		version, err = EvalJS(result.Page, "window.testVersion")
		if err != nil {
			return err
		}
		flagResult, err = EvalJS(result.Page, "window.testFlag")
		return err
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
	if version != "3.6.0" {
		t.Errorf("expected version '3.6.0', got %q", version)
	}
	if flagResult != "true" {
		t.Errorf("expected 'true', got %q", flagResult)
	}
}

func TestEvalJSUndefined(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		val, err := EvalJS(result.Page, "window.nonExistent")
		if err != nil {
			return err
		}
		if val != "" {
			t.Errorf("expected empty string for undefined, got %q", val)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
}

func TestScreenshot(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		data, err := Screenshot(result.Page)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			t.Error("expected non-empty screenshot")
		}
		// PNG magic bytes
		if data[0] != 0x89 || data[1] != 'P' || data[2] != 'N' || data[3] != 'G' {
			t.Error("expected PNG format")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
}

func TestExtractDOM(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		dom, err := ExtractDOM(result.Page)
		if err != nil {
			return err
		}
		if dom == "" {
			t.Error("expected non-empty DOM extraction")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
}

func TestNavigateReturnsExternalHosts(t *testing.T) {
	// With httptest, all servers bind to 127.0.0.1 so we can't truly test
	// cross-host detection. We verify the mechanism works: a page with no
	// external resources returns an empty list.
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	var hosts []string
	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		hosts = result.ExternalHosts
		return nil
	})
	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}

	if len(hosts) != 0 {
		t.Errorf("expected no external hosts for self-contained page, got %v", hosts)
	}
}
