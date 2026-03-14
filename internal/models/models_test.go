package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestScanRequestJSON(t *testing.T) {
	req := ScanRequest{
		URL: "https://example.com",
		Options: &ScanOptions{
			MaxRedirects:     5,
			BrowserDetection: true,
			TimeoutSeconds:   30,
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ScanRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.URL != req.URL {
		t.Errorf("URL: got %q, want %q", got.URL, req.URL)
	}
	if got.Options.MaxRedirects != 5 {
		t.Errorf("MaxRedirects: got %d, want 5", got.Options.MaxRedirects)
	}
	if !got.Options.BrowserDetection {
		t.Error("BrowserDetection should be true")
	}
}

func TestScanRequestOmitEmptyOptions(t *testing.T) {
	req := ScanRequest{URL: "https://example.com"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["options"]; ok {
		t.Error("options should be omitted when nil")
	}
}

func TestChainedResponseBodyExcluded(t *testing.T) {
	title := "Test Page"
	resp := ChainedResponse{
		URL:          "https://example.com",
		StatusCode:   200,
		Headers:      map[string]string{"Server": "nginx"},
		Body:         []byte("<html>secret</html>"),
		Title:        &title,
		ResponseSize: 1234,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["body"]; ok {
		t.Error("Body should be excluded from JSON (json:\"-\")")
	}
	if m["status_code"].(float64) != 200 {
		t.Errorf("status_code: got %v, want 200", m["status_code"])
	}
	if m["title"].(string) != "Test Page" {
		t.Errorf("title: got %v, want 'Test Page'", m["title"])
	}
}

func TestScanResultJSON(t *testing.T) {
	sitemap := "https://example.com/sitemap.xml"
	now := time.Date(2026, 3, 12, 10, 30, 0, 0, time.UTC)

	result := ScanResult{
		URL: "https://example.com",
		Chain: []ChainedResponse{
			{URL: "https://example.com", StatusCode: 301, ResponseSize: 0},
			{URL: "https://www.example.com/", StatusCode: 200, ResponseSize: 45230},
		},
		Technologies: []Technology{
			{Name: "WordPress", Version: "6.3.1", Category: "CMS"},
		},
		Cookies:   map[string]string{"session": "abc123"},
		Metadata:  &ScanMetadata{RobotsTXT: true, Sitemap: &sitemap},
		ScannedAt: now,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ScanResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Chain) != 2 {
		t.Errorf("Chain length: got %d, want 2", len(got.Chain))
	}
	if len(got.Technologies) != 1 {
		t.Errorf("Technologies length: got %d, want 1", len(got.Technologies))
	}
	if got.Technologies[0].Name != "WordPress" {
		t.Errorf("Technology name: got %q, want 'WordPress'", got.Technologies[0].Name)
	}
	if got.Cookies["session"] != "abc123" {
		t.Errorf("Cookie: got %q, want 'abc123'", got.Cookies["session"])
	}
	if !got.ScannedAt.Equal(now) {
		t.Errorf("ScannedAt: got %v, want %v", got.ScannedAt, now)
	}
}

func TestTechnologyOmitEmptyVersion(t *testing.T) {
	tech := Technology{Name: "Nginx", Category: "Server"}
	data, err := json.Marshal(tech)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["version"]; ok {
		t.Error("version should be omitted when empty")
	}
}

func TestDetectionResultJSON(t *testing.T) {
	dr := DetectionResult{
		Detected: true,
		Version:  "6.3.1",
		Evidence: "meta generator tag",
	}

	data, err := json.Marshal(dr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got DetectionResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !got.Detected {
		t.Error("Detected should be true")
	}
	if got.Version != "6.3.1" {
		t.Errorf("Version: got %q, want '6.3.1'", got.Version)
	}
}
