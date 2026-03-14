package models

import (
	"net/http"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"
)

// ScanRequest represents an incoming scan request.
type ScanRequest struct {
	URL     string       `json:"url"`
	Options *ScanOptions `json:"options,omitempty"`
}

// ScanOptions configures the scan behavior.
type ScanOptions struct {
	MaxRedirects     int  `json:"max_redirects,omitempty"`
	BrowserDetection bool `json:"browser_detection"`
	TimeoutSeconds   int  `json:"timeout_seconds,omitempty"`
}

// ScanResult is the complete output of a scan.
type ScanResult struct {
	URL           string            `json:"url"`
	Chain         []ChainedResponse `json:"chain"`
	Technologies  []Technology      `json:"technologies"`
	Cookies       map[string]string `json:"cookies"`
	Metadata      *ScanMetadata     `json:"metadata"`
	ExternalHosts []string          `json:"external_hosts"`
	ScannedAt     time.Time         `json:"scanned_at"`
}

// ChainedResponse captures data from a single hop in the redirect chain.
type ChainedResponse struct {
	URL          string            `json:"url"`
	StatusCode   int               `json:"status_code"`
	Headers      map[string]string `json:"headers"`
	RawHeaders   http.Header       `json:"-"`
	Body         []byte            `json:"-"`
	Title        *string           `json:"title"`
	ResponseSize int               `json:"response_size"`
}

// Technology represents a detected technology.
type Technology struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Category string `json:"category"`
}

// ScanMetadata holds additional metadata about the target.
type ScanMetadata struct {
	RobotsTXT bool    `json:"robots_txt"`
	Sitemap   *string `json:"sitemap"`
	Favicon   *string `json:"favicon"`
}

// DetectionContext provides all data available to a detector.
type DetectionContext struct {
	Responses   []ChainedResponse
	Document    *goquery.Document
	HTTPClient  *http.Client
	Browser     *rod.Browser
	BrowserPage *rod.Page
	BaseURL     string
}

// Detector is the interface for complex Go-based detectors.
type Detector interface {
	Name() string
	Category() string
	Detect(ctx *DetectionContext) (*DetectionResult, error)
}

// DetectionResult is the output of a single detection check.
type DetectionResult struct {
	Detected bool   `json:"detected"`
	Version  string `json:"version,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}
