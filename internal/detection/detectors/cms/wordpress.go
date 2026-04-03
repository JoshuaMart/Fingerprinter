package cms

import (
	"regexp"
	"strings"

	"github.com/JoshuaMart/fingerprinter/internal/chain"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

var (
	wpHeaderVersionRe = regexp.MustCompile(`([\d.]+)`)
	wpLinkAPIRe       = regexp.MustCompile(`rel="https://api\.w\.org/"`)
	wpPingbackRe      = regexp.MustCompile(`/xmlrpc\.php`)
	wpPoweredByRe     = regexp.MustCompile(`(?i)WordPress`)

	wpBodyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`<link[^>]+wp-(content|includes)/`),
		regexp.MustCompile(`<script[^>]+wp-(content|includes)/`),
		regexp.MustCompile(`<script[^>]+wp-embed\.min\.js`),
		regexp.MustCompile(`class="[^"]*wp-block-`),
	}
	wpEmbedVersionRe = regexp.MustCompile(`wp-embed\.min\.js\?ver=([\d.]+)`)
	wpEmojiVersionRe = regexp.MustCompile(`wp-emoji-release\.min\.js\?ver=([\d.]+)`)

	wpMetaGeneratorRe = regexp.MustCompile(`(?i)WordPress`)
	wpMetaVersionRe   = regexp.MustCompile(`([\d.]+)`)

	wpFeedVersionRe = regexp.MustCompile(`<generator[^>]+version="([\d.]+)"`)

	wpOPMLVersionRe       = regexp.MustCompile(`WordPress/([\d.]+)`)
	wpReadmeVersionRe     = regexp.MustCompile(`(?i)Version\s+([\d.]+)`)
	wpLoginAssetVersionRe = regexp.MustCompile(`\?ver=([\d.]+)`)
)

var wpJSExpressions = []string{
	`typeof wp !== 'undefined' && typeof wp.ajax !== 'undefined'`,
	`typeof wp !== 'undefined' && typeof wp.receiveEmbedMessage !== 'undefined'`,
	`typeof wp_username !== 'undefined'`,
}

var wpCookieNames = []string{
	"wordpress_logged_in",
	"wp-settings",
	"wp_woocommerce_session",
}

// WordPressDetector detects WordPress CMS.
type WordPressDetector struct{}

func (d *WordPressDetector) Name() string     { return "WordPress" }
func (d *WordPressDetector) Category() string { return "CMS" }

// JSExpressions returns JS expressions to pre-evaluate in the browser.
func (d *WordPressDetector) JSExpressions() []string { return wpJSExpressions }

func (d *WordPressDetector) Detect(ctx *models.DetectionContext) (*models.DetectionResult, error) {
	detected := false
	version := ""
	proof := &models.Proof{}

	// 1. Headers
	for _, resp := range ctx.Responses {
		if v := resp.RawHeaders.Get("x-wordpress-version"); v != "" {
			if m := wpHeaderVersionRe.FindStringSubmatch(v); m != nil {
				version = m[1]
			}
			detected = true
			proof.Headers = appendUniqueStr(proof.Headers, "x-wordpress-version")
		}
		if v := resp.RawHeaders.Get("link"); v != "" && wpLinkAPIRe.MatchString(v) {
			detected = true
			proof.Headers = appendUniqueStr(proof.Headers, "link")
		}
		if v := resp.RawHeaders.Get("x-pingback"); v != "" && wpPingbackRe.MatchString(v) {
			detected = true
			proof.Headers = appendUniqueStr(proof.Headers, "x-pingback")
		}
		if v := resp.RawHeaders.Get("x-powered-by"); v != "" && wpPoweredByRe.MatchString(v) {
			detected = true
			proof.Headers = appendUniqueStr(proof.Headers, "x-powered-by")
		}
	}

	// 2. Body
	for _, resp := range ctx.Responses {
		body := resp.Body
		for _, re := range wpBodyPatterns {
			if re.Match(body) {
				detected = true
				proof.Body = appendUniqueStr(proof.Body, re.String())
			}
		}
		if version == "" {
			if m := wpEmbedVersionRe.FindSubmatch(body); m != nil {
				version = string(m[1])
				detected = true
				proof.Body = appendUniqueStr(proof.Body, wpEmbedVersionRe.String())
			}
		}
		if version == "" {
			if m := wpEmojiVersionRe.FindSubmatch(body); m != nil {
				version = string(m[1])
				detected = true
				proof.Body = appendUniqueStr(proof.Body, wpEmojiVersionRe.String())
			}
		}
	}

	// 3. Meta (generator)
	if ctx.Document != nil {
		metas := chain.ExtractMeta(ctx.Document)
		if gen, ok := metas["generator"]; ok && wpMetaGeneratorRe.MatchString(gen) {
			detected = true
			proof.Meta = appendUniqueStr(proof.Meta, "generator")
			if version == "" {
				if m := wpMetaVersionRe.FindStringSubmatch(gen); m != nil {
					version = m[1]
				}
			}
		}
	}

	// 4. Cookies (prefix match — e.g. wp-settings-1 matches wp-settings)
	cookies := ctx.Cookies
	if cookies == nil {
		cookies = chain.ExtractCookies(ctx.Responses)
	}
	for cookieName := range cookies {
		for _, prefix := range wpCookieNames {
			if strings.HasPrefix(cookieName, prefix) {
				detected = true
				proof.Cookies = appendUniqueStr(proof.Cookies, prefix)
			}
		}
	}

	// 5. JS (pre-evaluated)
	for _, expr := range wpJSExpressions {
		if v, ok := ctx.JSResults[expr]; ok && v != "" && v != "false" && v != "undefined" && v != "null" {
			detected = true
			proof.JS = appendUniqueStr(proof.JS, expr)
		}
	}

	// 6. Conditional path probes (only if detected but no version)
	if detected && version == "" && ctx.BrowserPool != nil && ctx.BaseURL != "" {
		base := strings.TrimRight(ctx.BaseURL, "/")

		// Try feed=atom
		resp, err := ctx.BrowserPool.NavigateAndCapture(ctx.Ctx, base+"/?feed=atom")
		if err == nil && resp.StatusCode == 200 {
			if m := wpFeedVersionRe.FindSubmatch(resp.Body); m != nil {
				version = string(m[1])
				proof.Probe = append(proof.Probe, "/?feed=atom")
			}
		}

		// If still no version, try wp-links-opml
		if version == "" {
			resp, err = ctx.BrowserPool.NavigateAndCapture(ctx.Ctx, base+"/wp-links-opml.php")
			if err == nil && resp.StatusCode == 200 {
				if m := wpOPMLVersionRe.FindSubmatch(resp.Body); m != nil {
					version = string(m[1])
					proof.Probe = append(proof.Probe, "wp-links-opml")
				}
			}
		}

		// Try readme.html
		if version == "" {
			resp, err = ctx.BrowserPool.NavigateAndCapture(ctx.Ctx, base+"/readme.html")
			if err == nil && resp.StatusCode == 200 {
				if m := wpReadmeVersionRe.FindSubmatch(resp.Body); m != nil {
					version = string(m[1])
					proof.Probe = append(proof.Probe, "readme.html")
				}
			}
		}

		// Try wp-login.php assets ver= param
		if version == "" {
			resp, err = ctx.BrowserPool.NavigateAndCapture(ctx.Ctx, base+"/wp-login.php")
			if err == nil && resp.StatusCode == 200 {
				if m := wpLoginAssetVersionRe.FindSubmatch(resp.Body); m != nil {
					version = string(m[1])
					proof.Probe = append(proof.Probe, "wp-login.php")
				}
			}
		}
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

func appendUniqueStr(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}
