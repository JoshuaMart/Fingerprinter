package cms

import (
	"context"
	"regexp"
	"strings"

	"github.com/JoshuaMart/fingerprinter/internal/chain"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

var (
	drupalHeaderGeneratorRe = regexp.MustCompile(`(?i)Drupal\s(\d+)`)
	drupalHeaderVersionRe   = regexp.MustCompile(`(\d+)`)

	drupalBodyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`<(?:link|style)[^>]+"/sites/(?:default|all)/(?:themes|modules)/`),
		regexp.MustCompile(`<[^>]+data-drupal-(?:link-system-path|selector)=`),
		regexp.MustCompile(`drupal(?:\.init)?\.js`),
	}

	drupalBodyVersionRe = regexp.MustCompile(`drupal(?:\.init)?\.js\?v=((?:8|9|1[0-9])(?:\.\d+)*(?:-\w+)?)`)

	drupalMetaGeneratorRe = regexp.MustCompile(`(?i)Drupal`)
	drupalMetaVersionRe   = regexp.MustCompile(`(\d+)`)

	drupalCookieRe = regexp.MustCompile(`^SESS[a-f0-9]{32}$`)

	drupalChangelogVersionRe     = regexp.MustCompile(`Drupal\s(\d+(?:\.\d+)*)`)
	drupalCoreChangelogVersionRe = regexp.MustCompile(`Drupal\s(\d+)`)
	drupalInstallVersionRe       = regexp.MustCompile(`install\.js\?v=(\d+[\d.]*(?:-\w+)?)`)
)

var drupalJSExpressions = []string{
	`typeof Drupal !== 'undefined' && typeof Drupal.behaviors !== 'undefined'`,
}

var drupalDetectionHeaders = []string{
	"x-drupal-cache",
	"x-drupal-dynamic-cache",
	"x-drupal-route-normalizer",
}

// DrupalDetector detects Drupal CMS.
type DrupalDetector struct{}

func (d *DrupalDetector) Name() string     { return "Drupal" }
func (d *DrupalDetector) Category() string { return "CMS" }

// JSExpressions returns JS expressions to pre-evaluate in the browser.
func (d *DrupalDetector) JSExpressions() []string { return drupalJSExpressions }

func (d *DrupalDetector) Detect(ctx *models.DetectionContext) (*models.DetectionResult, error) {
	detected := false
	version := ""
	proof := &models.Proof{}

	// 1. Headers
	for _, resp := range ctx.Responses {
		// X-Generator: Drupal 10 (https://www.drupal.org)
		if v := resp.RawHeaders.Get("x-generator"); v != "" && drupalHeaderGeneratorRe.MatchString(v) {
			detected = true
			proof.Headers = appendUniqueStr(proof.Headers, "x-generator")
			if version == "" {
				if m := drupalHeaderVersionRe.FindStringSubmatch(v); m != nil {
					version = m[1]
				}
			}
		}

		// Drupal-specific headers (presence only)
		for _, h := range drupalDetectionHeaders {
			if v := resp.RawHeaders.Get(h); v != "" {
				detected = true
				proof.Headers = appendUniqueStr(proof.Headers, h)
			}
		}

		// Expires: 19 Nov 1978 (Drupal's famous birth date)
		if v := resp.RawHeaders.Get("expires"); strings.Contains(v, "19 Nov 1978") {
			detected = true
			proof.Headers = appendUniqueStr(proof.Headers, "expires")
		}
	}

	// 2. Body
	for _, resp := range ctx.Responses {
		for _, re := range drupalBodyPatterns {
			if re.Match(resp.Body) {
				detected = true
				proof.Body = appendUniqueStr(proof.Body, re.String())
			}
		}
		if version == "" {
			if m := drupalBodyVersionRe.FindSubmatch(resp.Body); m != nil {
				version = string(m[1])
				detected = true
				proof.Body = appendUniqueStr(proof.Body, drupalBodyVersionRe.String())
			}
		}
	}

	// 3. Meta (generator)
	if ctx.Document != nil {
		metas := chain.ExtractMeta(ctx.Document)
		if gen, ok := metas["generator"]; ok && drupalMetaGeneratorRe.MatchString(gen) {
			detected = true
			proof.Meta = appendUniqueStr(proof.Meta, "generator")
			if version == "" {
				if m := drupalMetaVersionRe.FindStringSubmatch(gen); m != nil {
					version = m[1]
				}
			}
		}
	}

	// 4. Cookies (SESS + 32 hex chars)
	cookies := chain.ExtractCookies(ctx.Responses)
	for cookieName := range cookies {
		if drupalCookieRe.MatchString(cookieName) {
			detected = true
			proof.Cookies = appendUniqueStr(proof.Cookies, cookieName)
		}
	}

	// 5. JS (pre-evaluated)
	for _, expr := range drupalJSExpressions {
		if v, ok := ctx.JSResults[expr]; ok && v != "" && v != "false" && v != "undefined" && v != "null" {
			detected = true
			proof.JS = appendUniqueStr(proof.JS, expr)
		}
	}

	// 6. Path probes for version (only if detected but no version)
	if detected && version == "" && !ctx.SkipPathChecks && ctx.BrowserPool != nil && ctx.BaseURL != "" {
		version = d.probeVersion(ctx, proof)
	}

	if !detected {
		return &models.DetectionResult{Detected: false}, nil
	}

	return &models.DetectionResult{
		Detected: true,
		Version:  version,
		Proof:    proof,
	}, nil
}

func (d *DrupalDetector) probeVersion(ctx *models.DetectionContext, proof *models.Proof) string {
	base := strings.TrimRight(ctx.BaseURL, "/")

	// CHANGELOG.txt (Drupal 7 and older)
	resp, err := ctx.BrowserPool.NavigateAndCapture(context.Background(), base+"/CHANGELOG.txt")
	if err == nil && resp.StatusCode == 200 {
		if m := drupalChangelogVersionRe.FindSubmatch(resp.Body); m != nil {
			proof.Probe = append(proof.Probe, "CHANGELOG.txt")
			return string(m[1])
		}
	}

	// core/install.php (Drupal 8+)
	resp, err = ctx.BrowserPool.NavigateAndCapture(context.Background(), base+"/core/install.php")
	if err == nil && resp.StatusCode == 200 {
		if m := drupalInstallVersionRe.FindSubmatch(resp.Body); m != nil {
			proof.Probe = append(proof.Probe, "core/install.php")
			return string(m[1])
		}
	}

	// core/CHANGELOG.txt (Drupal 8+)
	resp, err = ctx.BrowserPool.NavigateAndCapture(context.Background(), base+"/core/CHANGELOG.txt")
	if err == nil && resp.StatusCode == 200 {
		if m := drupalCoreChangelogVersionRe.FindSubmatch(resp.Body); m != nil {
			proof.Probe = append(proof.Probe, "core/CHANGELOG.txt")
			return string(m[1])
		}
	}

	return ""
}
