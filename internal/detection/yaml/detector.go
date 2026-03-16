package yaml

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/JoshuaMart/fingerprinter/internal/chain"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

// Detector wraps a YAML Definition and implements models.Detector.
type Detector struct {
	def Definition
}

// NewDetector creates a Detector from a YAML Definition.
func NewDetector(def Definition) *Detector {
	return &Detector{def: def}
}

func (d *Detector) Name() string     { return d.def.Name }
func (d *Detector) Category() string { return d.def.Category }

// JSExpressions returns all JS expressions this detector needs evaluated.
func (d *Detector) JSExpressions() []string {
	exprs := make([]string, len(d.def.Checks.JS))
	for i, js := range d.def.Checks.JS {
		exprs[i] = js.Expression
	}
	return exprs
}

// Detect runs all checks defined in the YAML and returns a result.
func (d *Detector) Detect(ctx *models.DetectionContext) (*models.DetectionResult, error) {
	var matches int
	var total int
	var version string

	// Headers checks — run against all responses in the chain
	for headerName, check := range d.def.Checks.Headers {
		total++
		if v, ok := matchHeaders(ctx.Responses, headerName, check); ok {
			matches++
			if v != "" && version == "" {
				version = v
			}
		}
	}

	// Body checks — run against all responses in the chain
	for _, check := range d.def.Checks.Body {
		total++
		if v, ok := matchBody(ctx.Responses, check); ok {
			matches++
			if v != "" && version == "" {
				version = v
			}
		}
	}

	// Meta checks — run against parsed DOM (final response)
	if ctx.Document != nil {
		metas := chain.ExtractMeta(ctx.Document)
		for metaName, check := range d.def.Checks.Meta {
			total++
			if v, ok := matchMeta(metas, metaName, check); ok {
				matches++
				if v != "" && version == "" {
					version = v
				}
			}
		}
	}

	// Cookie checks — run against all responses in the chain
	if len(d.def.Checks.Cookies) > 0 {
		cookies := chain.ExtractCookies(ctx.Responses)
		for cookieName, check := range d.def.Checks.Cookies {
			total++
			if matchCookie(cookies, cookieName, check) {
				matches++
			}
		}
	}

	// Path checks — navigate via browser pool
	for _, check := range d.def.Checks.Paths {
		total++
		if matchPath(ctx.BrowserPool, ctx.BaseURL, check) {
			matches++
		}
	}

	// JS checks — require browser page, skip if unavailable
	for _, check := range d.def.Checks.JS {
		if ctx.BrowserPage == nil {
			continue
		}
		total++
		if v, ok := matchJS(ctx, check); ok {
			matches++
			if v != "" && version == "" {
				version = v
			}
		}
	}

	// Favicon hash checks — compute hash once, compare against all expected hashes
	if len(d.def.Checks.FaviconHash) > 0 {
		total++
		if hash, ok := faviconMMH3(ctx.HTTPClient, ctx.BaseURL, ctx.Document); ok {
			for _, expected := range d.def.Checks.FaviconHash {
				if hash == expected {
					matches++
					break
				}
			}
		}
	}

	if total == 0 || matches == 0 {
		return &models.DetectionResult{Detected: false}, nil
	}

	return &models.DetectionResult{
		Detected: true,
		Version:  version,
		Evidence: fmt.Sprintf("%d/%d checks matched", matches, total),
	}, nil
}

func matchHeaders(responses []models.ChainedResponse, headerName string, check HeaderCheck) (string, bool) {
	re, err := regexp.Compile(check.Pattern)
	if err != nil {
		return "", false
	}
	for _, resp := range responses {
		value := resp.RawHeaders.Get(headerName)
		if value == "" {
			continue
		}
		if re.MatchString(value) {
			return extractVersion(value, check.Version), true
		}
	}
	return "", false
}

func matchBody(responses []models.ChainedResponse, check BodyCheck) (string, bool) {
	re, err := regexp.Compile(check.Pattern)
	if err != nil {
		return "", false
	}
	for _, resp := range responses {
		body := string(resp.Body)
		if re.MatchString(body) {
			return extractVersion(body, check.Version), true
		}
	}
	return "", false
}

func matchMeta(metas map[string]string, metaName string, check MetaCheck) (string, bool) {
	value, exists := metas[strings.ToLower(metaName)]
	if !exists {
		return "", false
	}
	re, err := regexp.Compile(check.Pattern)
	if err != nil {
		return "", false
	}
	if re.MatchString(value) {
		return extractVersion(value, check.Version), true
	}
	return "", false
}

func matchCookie(cookies map[string]string, cookieName string, check CookieCheck) bool {
	_, exists := cookies[cookieName]
	if !exists {
		return false
	}
	// If no pattern, just check the cookie name exists
	if check.Pattern == "" {
		return true
	}
	re, err := regexp.Compile(check.Pattern)
	if err != nil {
		return false
	}
	return re.MatchString(cookies[cookieName])
}

func matchPath(navigator models.BrowserNavigator, baseURL string, check PathCheck) bool {
	if navigator == nil {
		return false
	}
	u := strings.TrimRight(baseURL, "/") + check.Path
	resp, err := navigator.NavigateAndCapture(context.Background(), u)
	if err != nil {
		return false
	}
	return resp.StatusCode == check.Status
}

func matchJS(ctx *models.DetectionContext, check JSCheck) (string, bool) {
	// Use pre-evaluated cached results (preferred — page may be closed)
	if ctx.JSResults != nil {
		if val, ok := ctx.JSResults[check.Expression]; ok {
			if check.Version {
				return val, true
			}
			return "", true
		}
	}

	// Fallback to live evaluation
	if ctx.BrowserPage == nil {
		return "", false
	}

	result, err := ctx.BrowserPage.Eval("() => { try { return " + check.Expression + " } catch(e) { return undefined } }")
	if err != nil {
		slog.Warn("JS eval failed", "expression", check.Expression, "error", err)
		return "", false
	}
	if result == nil || result.Value.Nil() {
		return "", false
	}

	if check.Version {
		return result.Value.String(), true
	}
	return "", true
}

// extractVersion applies the version regex on the same value that pattern matched against.
// The version field is a regex with a capture group — the first group is returned as the version.
func extractVersion(value string, versionPattern string) string {
	if versionPattern == "" {
		return ""
	}
	re, err := regexp.Compile(versionPattern)
	if err != nil {
		return ""
	}
	matches := re.FindStringSubmatch(value)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}
