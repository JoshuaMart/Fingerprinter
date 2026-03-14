package chain

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JoshuaMart/fingerprinter/internal/httpclient"
)

func defaultCfg() Config {
	return Config{
		MaxRedirects: 10,
		Headers:      map[string]string{"User-Agent": "TestAgent/1.0"},
	}
}

func testClient() *http.Client {
	return httpclient.New(httpclient.Config{Timeout: 5 * time.Second})
}

func TestSimpleChain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><head><title>Final Page</title></head><body>Hello</body></html>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	chain, err := Follow(context.Background(), srv.URL+"/", defaultCfg(), testClient())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chain) != 2 {
		t.Fatalf("expected 2 hops, got %d", len(chain))
	}

	if chain[0].StatusCode != 301 {
		t.Errorf("first hop: expected 301, got %d", chain[0].StatusCode)
	}
	if chain[1].StatusCode != 200 {
		t.Errorf("second hop: expected 200, got %d", chain[1].StatusCode)
	}
	if chain[1].Title == nil || *chain[1].Title != "Final Page" {
		t.Errorf("expected title 'Final Page', got %v", chain[1].Title)
	}
	if chain[1].ResponseSize == 0 {
		t.Error("expected non-zero response size for final hop")
	}
}

func TestNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><head><title>Direct</title></head></html>")
	}))
	defer srv.Close()

	chain, err := Follow(context.Background(), srv.URL, defaultCfg(), testClient())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chain) != 1 {
		t.Fatalf("expected 1 hop, got %d", len(chain))
	}
	if chain[0].StatusCode != 200 {
		t.Errorf("expected 200, got %d", chain[0].StatusCode)
	}
}

func TestMaxRedirectsExceeded(t *testing.T) {
	counter := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter++
		http.Redirect(w, r, fmt.Sprintf("/redirect-%d", counter), http.StatusFound)
	}))
	defer srv.Close()

	cfg := defaultCfg()
	cfg.MaxRedirects = 3

	_, err := Follow(context.Background(), srv.URL, cfg, testClient())
	if err != ErrMaxRedirects {
		t.Fatalf("expected ErrMaxRedirects, got %v", err)
	}
}

func TestCircularRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", http.StatusFound)
		} else {
			http.Redirect(w, r, "/a", http.StatusFound)
		}
	}))
	defer srv.Close()

	_, err := Follow(context.Background(), srv.URL+"/a", defaultCfg(), testClient())
	if err != ErrCircularRedirect {
		t.Fatalf("expected ErrCircularRedirect, got %v", err)
	}
}

func TestMissingLocationHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()

	_, err := Follow(context.Background(), srv.URL, defaultCfg(), testClient())
	if err != ErrMissingLocation {
		t.Fatalf("expected ErrMissingLocation, got %v", err)
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = fmt.Fprint(w, "too slow")
	}))
	defer srv.Close()

	client := httpclient.New(httpclient.Config{Timeout: 100 * time.Millisecond})
	_, err := Follow(context.Background(), srv.URL, defaultCfg(), client)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestCustomHeaders(t *testing.T) {
	var receivedUA string
	var receivedCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		receivedCustom = r.Header.Get("X-Custom")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	cfg := defaultCfg()
	cfg.Headers["X-Custom"] = "my-value"

	_, err := Follow(context.Background(), srv.URL, cfg, testClient())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedUA != "TestAgent/1.0" {
		t.Errorf("User-Agent: got %q, want 'TestAgent/1.0'", receivedUA)
	}
	if receivedCustom != "my-value" {
		t.Errorf("X-Custom: got %q, want 'my-value'", receivedCustom)
	}
}

func TestRelativeRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/end")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/end", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "done")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	chain, err := Follow(context.Background(), srv.URL+"/start", defaultCfg(), testClient())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chain) != 2 {
		t.Fatalf("expected 2 hops, got %d", len(chain))
	}
	if chain[1].StatusCode != 200 {
		t.Errorf("expected final 200, got %d", chain[1].StatusCode)
	}
}

func TestNonHTMLNoTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":"test"}`)
	}))
	defer srv.Close()

	chain, err := Follow(context.Background(), srv.URL, defaultCfg(), testClient())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if chain[0].Title != nil {
		t.Errorf("expected nil title for JSON response, got %v", *chain[0].Title)
	}
}

func TestMultipleRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/2", http.StatusFound)
	})
	mux.HandleFunc("/2", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/3", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/3", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><head><title>Page 3</title></head></html>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	chain, err := Follow(context.Background(), srv.URL+"/1", defaultCfg(), testClient())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chain) != 3 {
		t.Fatalf("expected 3 hops, got %d", len(chain))
	}
	if chain[0].StatusCode != 302 {
		t.Errorf("hop 1: expected 302, got %d", chain[0].StatusCode)
	}
	if chain[1].StatusCode != 307 {
		t.Errorf("hop 2: expected 307, got %d", chain[1].StatusCode)
	}
	if chain[2].StatusCode != 200 {
		t.Errorf("hop 3: expected 200, got %d", chain[2].StatusCode)
	}
}

func TestValidateURLValid(t *testing.T) {
	valid := []string{
		"http://example.com",
		"https://example.com",
		"https://example.com:8443/path",
		"http://sub.domain.example.com",
	}
	for _, u := range valid {
		if err := ValidateURL(u); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", u, err)
		}
	}
}

func TestValidateURLBlocked(t *testing.T) {
	blocked := []string{
		"file:///etc/passwd",
		"ftp://example.com",
		"gopher://example.com",
		"",
		"not-a-url",
	}
	for _, u := range blocked {
		if err := ValidateURL(u); err == nil {
			t.Errorf("expected %q to be blocked", u)
		}
	}
}

func TestFollowBlocksFileScheme(t *testing.T) {
	_, err := Follow(context.Background(), "file:///etc/passwd", defaultCfg(), testClient())
	if err == nil {
		t.Error("expected file:// scheme to be blocked")
	}
}

func TestFollowBlocksFTPScheme(t *testing.T) {
	_, err := Follow(context.Background(), "ftp://example.com", defaultCfg(), testClient())
	if err == nil {
		t.Error("expected ftp:// scheme to be blocked")
	}
}
