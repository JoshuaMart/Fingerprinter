package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func controlURL() string {
	if v := os.Getenv("FINGERPRINTER_BROWSER_CONTROL_URL"); v != "" {
		return v
	}
	return "http://localhost:9222"
}

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
	pool, err := NewPool(2, 10*time.Second, controlURL())
	if err != nil {
		t.Skipf("browser not available, skipping: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func TestPoolCreateAndClose(t *testing.T) {
	_ = setupPool(t)
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

func TestNavigateCapturesChain(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		if len(result.Chain) == 0 {
			t.Error("expected at least one response in chain")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
}

func TestEvalJS(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		obj, err := result.Page.Eval("() => { try { return window.testVersion } catch(e) { return undefined } }")
		if err != nil {
			return err
		}
		if obj.Value.String() != "3.6.0" {
			t.Errorf("expected version '3.6.0', got %q", obj.Value.String())
		}

		obj, err = result.Page.Eval("() => { try { return window.testFlag } catch(e) { return undefined } }")
		if err != nil {
			return err
		}
		if obj.Value.String() != "true" {
			t.Errorf("expected 'true', got %q", obj.Value.String())
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
}

func TestEvalJSUndefined(t *testing.T) {
	srv := startTestServer()
	defer srv.Close()
	pool := setupPool(t)

	err := pool.Navigate(context.Background(), srv.URL, func(result *NavigateResult) error {
		obj, err := result.Page.Eval("() => { try { return window.nonExistent } catch(e) { return undefined } }")
		if err != nil {
			return err
		}
		if !obj.Value.Nil() {
			t.Errorf("expected nil for undefined, got %v", obj.Value)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}
}

func TestNavigateReturnsExternalHosts(t *testing.T) {
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
