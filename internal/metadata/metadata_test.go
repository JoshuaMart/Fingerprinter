package metadata

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestRobotsTXTPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = fmt.Fprint(w, "User-agent: *\nDisallow: /admin\n")
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	meta := Fetch(srv.Client(), srv.URL, nil)
	if !meta.RobotsTXT {
		t.Error("expected robots_txt to be true")
	}
}

func TestRobotsTXTAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	meta := Fetch(srv.Client(), srv.URL, nil)
	if meta.RobotsTXT {
		t.Error("expected robots_txt to be false")
	}
}

func TestSitemapFromRobotsTXT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = fmt.Fprint(w, "User-agent: *\nSitemap: https://example.com/sitemap.xml\n")
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	meta := Fetch(srv.Client(), srv.URL, nil)
	if meta.Sitemap == nil || *meta.Sitemap != "https://example.com/sitemap.xml" {
		t.Errorf("expected sitemap from robots.txt, got %v", meta.Sitemap)
	}
}

func TestSitemapFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			_, _ = fmt.Fprint(w, "User-agent: *\nDisallow:\n")
		case "/sitemap.xml":
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	meta := Fetch(srv.Client(), srv.URL, nil)
	if meta.Sitemap == nil {
		t.Fatal("expected sitemap via fallback")
	}
	if !strings.HasSuffix(*meta.Sitemap, "/sitemap.xml") {
		t.Errorf("expected sitemap URL ending in /sitemap.xml, got %q", *meta.Sitemap)
	}
}

func TestSitemapNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = fmt.Fprint(w, "User-agent: *\n")
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	meta := Fetch(srv.Client(), srv.URL, nil)
	if meta.Sitemap != nil {
		t.Errorf("expected nil sitemap, got %q", *meta.Sitemap)
	}
}

func TestFaviconFromHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><link rel="icon" href="/static/favicon.png"></head></html>`,
	))

	meta := Fetch(srv.Client(), srv.URL, doc)
	if meta.Favicon == nil {
		t.Fatal("expected favicon from HTML")
	}
	if !strings.HasSuffix(*meta.Favicon, "/static/favicon.png") {
		t.Errorf("expected favicon path, got %q", *meta.Favicon)
	}
}

func TestFaviconFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	meta := Fetch(srv.Client(), srv.URL, nil)
	if meta.Favicon == nil {
		t.Fatal("expected favicon via fallback")
	}
	if !strings.HasSuffix(*meta.Favicon, "/favicon.ico") {
		t.Errorf("expected favicon.ico URL, got %q", *meta.Favicon)
	}
}

func TestFaviconNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	meta := Fetch(srv.Client(), srv.URL, nil)
	if meta.Favicon != nil {
		t.Errorf("expected nil favicon, got %q", *meta.Favicon)
	}
}

func TestFaviconAbsoluteURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><link rel="icon" href="https://cdn.example.com/favicon.png"></head></html>`,
	))

	meta := Fetch(srv.Client(), srv.URL, doc)
	if meta.Favicon == nil || *meta.Favicon != "https://cdn.example.com/favicon.png" {
		t.Errorf("expected absolute favicon URL, got %v", meta.Favicon)
	}
}
