package yaml

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

	// Collect JS expressions to evaluate on path pages
	jsExprs := make([]string, len(d.def.Checks.JS))
	for i, js := range d.def.Checks.JS {
		jsExprs[i] = js.Expression
	}

	// Path checks — collect responses and JS results to feed into other checks (not counted as matches)
	var pathResponses []models.ChainedResponse
	pathJSResults := make(map[string]string)
	if !ctx.SkipPathChecks {
		for _, check := range d.def.Checks.Paths {
			if check.Browser {
				// Browser mode: navigate via browser, supports JS eval on path page
				resp, jsResults, ok := matchPath(ctx.BrowserPool, ctx.BaseURL, check, jsExprs)
				if ok && resp != nil {
					pathResponses = append(pathResponses, *resp)
					for k, v := range jsResults {
						if _, exists := pathJSResults[k]; !exists {
							pathJSResults[k] = v
						}
					}
				}
			} else {
				// HTTP mode (default): simple GET via HTTP client
				resp, ok := matchPathHTTP(ctx.HTTPClient, ctx.BaseURL, check)
				if ok && resp != nil {
					pathResponses = append(pathResponses, *resp)
				}
			}
		}
	}

	// Build combined response pool: initial responses + path responses
	allResponses := ctx.Responses
	if len(pathResponses) > 0 {
		allResponses = make([]models.ChainedResponse, 0, len(ctx.Responses)+len(pathResponses))
		allResponses = append(allResponses, ctx.Responses...)
		allResponses = append(allResponses, pathResponses...)
	}

	// Headers checks — run against all responses
	for headerName, check := range d.def.Checks.Headers {
		total++
		if v, ok := matchHeaders(allResponses, headerName, check); ok {
			matches++
			if v != "" && version == "" {
				version = v
			}
		}
	}

	// Body checks — run against all responses
	bodyChecks := d.def.Checks.Body
	if len(bodyChecks.Patterns) > 0 {
		if bodyChecks.Matcher == "all" {
			// All patterns must match — counts as a single check
			total++
			allMatched := true
			for _, check := range bodyChecks.Patterns {
				if v, ok := matchBody(allResponses, check); ok {
					if v != "" && version == "" {
						version = v
					}
				} else {
					allMatched = false
				}
			}
			if allMatched {
				matches++
			}
		} else {
			// Any pattern can match (default) — each pattern is an independent check
			for _, check := range bodyChecks.Patterns {
				total++
				if v, ok := matchBody(allResponses, check); ok {
					matches++
					if v != "" && version == "" {
						version = v
					}
				}
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

	// Cookie checks — run against all responses
	if len(d.def.Checks.Cookies) > 0 {
		cookies := chain.ExtractCookies(allResponses)
		for cookieName, check := range d.def.Checks.Cookies {
			total++
			if matchCookie(cookies, cookieName, check) {
				matches++
			}
		}
	}

	// JS checks — merge main page results with path page results
	allJSResults := ctx.JSResults
	if len(pathJSResults) > 0 {
		allJSResults = make(map[string]string)
		for k, v := range ctx.JSResults {
			allJSResults[k] = v
		}
		for k, v := range pathJSResults {
			if _, exists := allJSResults[k]; !exists {
				allJSResults[k] = v
			}
		}
	}
	for _, check := range d.def.Checks.JS {
		if ctx.BrowserPage == nil && len(allJSResults) == 0 {
			continue
		}
		total++
		if v, ok := matchJSWithResults(allJSResults, ctx, check); ok {
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
	for _, resp := range responses {
		value := resp.RawHeaders.Get(headerName)
		if value == "" {
			continue
		}
		// No pattern = presence check only
		if check.Pattern == "" {
			return "", true
		}
		re, err := regexp.Compile(check.Pattern)
		if err != nil {
			return "", false
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

func matchPath(navigator models.BrowserNavigator, baseURL string, check PathCheck, jsExprs []string) (*models.ChainedResponse, map[string]string, bool) {
	if navigator == nil {
		return nil, nil, false
	}
	u := strings.TrimRight(baseURL, "/") + check.Path

	if len(jsExprs) > 0 {
		resp, jsResults, err := navigator.NavigateCaptureAndEval(context.Background(), u, jsExprs)
		if err != nil {
			return nil, nil, false
		}
		if resp.StatusCode == check.Status {
			return resp, jsResults, true
		}
		return nil, nil, false
	}

	resp, err := navigator.NavigateAndCapture(context.Background(), u)
	if err != nil {
		return nil, nil, false
	}
	if resp.StatusCode == check.Status {
		return resp, nil, true
	}
	return nil, nil, false
}

func matchPathHTTP(httpClient *http.Client, baseURL string, check PathCheck) (*models.ChainedResponse, bool) {
	if httpClient == nil {
		return nil, false
	}
	u := strings.TrimRight(baseURL, "/") + check.Path

	resp, err := httpClient.Get(u) //nolint:noctx
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	cr := &models.ChainedResponse{
		URL:        u,
		StatusCode: resp.StatusCode,
		Body:       body,
		RawHeaders: resp.Header,
	}
	if resp.StatusCode == check.Status {
		return cr, true
	}
	return nil, false
}

func matchJSWithResults(jsResults map[string]string, ctx *models.DetectionContext, check JSCheck) (string, bool) {
	// Use pre-evaluated cached results (main page + path pages)
	if jsResults != nil {
		if val, ok := jsResults[check.Expression]; ok {
			if check.Version {
				return val, true
			}
			return "", true
		}
	}

	// Fallback to live evaluation on the main page
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
