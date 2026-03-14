package yaml

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBase64RFC2045(t *testing.T) {
	// Known test: Shodan uses RFC 2045 base64 with 76-char lines
	data := []byte("hello world")
	got := base64RFC2045(data)
	want := "aGVsbG8gd29ybGQ=\n"
	if got != want {
		t.Errorf("base64RFC2045(%q) = %q, want %q", data, got, want)
	}
}

func TestBase64RFC2045LineWrapping(t *testing.T) {
	// Generate data that produces >76 chars of base64
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	encoded := base64RFC2045(data)

	lines := 0
	for _, line := range splitLines(encoded) {
		if line == "" {
			continue
		}
		if len(line) > 76 {
			t.Errorf("line length %d exceeds 76 chars", len(line))
		}
		lines++
	}
	if lines == 0 {
		t.Error("expected at least one line")
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestFaviconMMH3(t *testing.T) {
	// Serve a known favicon and verify the hash
	faviconData := []byte{0x00, 0x00, 0x01, 0x00} // minimal ICO header bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/x-icon")
			_, _ = w.Write(faviconData)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	hash, ok := faviconMMH3(srv.Client(), srv.URL, nil)
	if !ok {
		t.Fatal("expected hash computation to succeed")
	}

	// Verify it's a deterministic non-zero hash
	if hash == 0 {
		t.Error("expected non-zero hash")
	}

	// Verify reproducibility
	hash2, ok := faviconMMH3(srv.Client(), srv.URL, nil)
	if !ok || hash != hash2 {
		t.Error("expected reproducible hash")
	}
}

func TestFaviconMMH3NoServer(t *testing.T) {
	_, ok := faviconMMH3(nil, "", nil)
	if ok {
		t.Error("expected false when no client")
	}
}

func TestFaviconMMH3_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, ok := faviconMMH3(srv.Client(), srv.URL, nil)
	if ok {
		t.Error("expected false when favicon returns 404")
	}
}
