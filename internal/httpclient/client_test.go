package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSameHostRedirectFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(Config{Timeout: 5 * time.Second})
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200 (redirect followed), got %d", resp.StatusCode)
	}
}

func TestCrossHostRedirectStopped(t *testing.T) {
	// Target server redirects to a different host
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer other.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/landed", http.StatusFound)
	}))
	defer srv.Close()

	client := New(Config{Timeout: 5 * time.Second})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()

	// Should get the 302 back, not follow it
	if resp.StatusCode != 302 {
		t.Errorf("expected 302 (cross-host redirect stopped), got %d", resp.StatusCode)
	}
}

func TestNoRedirectClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	base := New(Config{Timeout: 5 * time.Second})
	client := NoRedirect(base)

	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()

	// Should NOT follow even same-host redirect
	if resp.StatusCode != 302 {
		t.Errorf("expected 302 (no redirect), got %d", resp.StatusCode)
	}
}
