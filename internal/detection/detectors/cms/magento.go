package cms

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

var (
	magentoHeaderPatterns = map[string]*regexp.Regexp{
		"x-magento-debug":         regexp.MustCompile(`\d`),
		"x-magento-cache-control": regexp.MustCompile(`\w`),
	}

	magentoBodyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`Magento_PageCache`),
		regexp.MustCompile(`Mage\.Cookies\.path`),
		regexp.MustCompile(`data-requiremodule="(mage|Magento_)`),
		regexp.MustCompile(`MAGENTO_`),
		regexp.MustCompile(`Magento Security Scan`),
		regexp.MustCompile(`x-magento-ini`),
		regexp.MustCompile(`js/mage`),
		regexp.MustCompile(`mage/cookies`),
	}

	magentoMetaPattern = regexp.MustCompile(`(?i)Magento`)
)

const (
	graphQLPath  = "/graphql"
	graphQLQuery = "{customerDownloadableProducts{items{date download_url}}}"
)

// MagentoDetector detects Magento e-commerce platform.
type MagentoDetector struct{}

func (d *MagentoDetector) Name() string     { return "Magento" }
func (d *MagentoDetector) Category() string { return "E-commerce" }

func (d *MagentoDetector) Detect(ctx *models.DetectionContext) (*models.DetectionResult, error) {
	proof := &models.Proof{}
	cheapMatch := false

	// Header checks (cheap)
	for headerName, re := range magentoHeaderPatterns {
		for _, resp := range ctx.Responses {
			if value := resp.RawHeaders.Get(headerName); value != "" && re.MatchString(value) {
				cheapMatch = true
				proof.Headers = appendUniqueStr(proof.Headers, headerName)
			}
		}
	}

	// Body checks (cheap)
	for _, re := range magentoBodyPatterns {
		for _, resp := range ctx.Responses {
			if re.Match(resp.Body) {
				cheapMatch = true
				proof.Body = appendUniqueStr(proof.Body, re.String())
			}
		}
	}

	// Meta content check (cheap)
	if ctx.Document != nil && matchMagentoMeta(ctx.Document) {
		cheapMatch = true
		proof.Meta = append(proof.Meta, "generator")
	}

	if cheapMatch {
		return &models.DetectionResult{Detected: true, Proof: proof}, nil
	}

	// GraphQL endpoint check (expensive — only if no cheap check matched)
	if !ctx.SkipPathChecks && checkMagentoGraphQL(ctx.HTTPClient, ctx.BaseURL) {
		proof.Probe = append(proof.Probe, "graphql")
		return &models.DetectionResult{Detected: true, Proof: proof}, nil
	}

	return &models.DetectionResult{Detected: false}, nil
}

func matchMagentoMeta(doc *goquery.Document) bool {
	found := false
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		if found {
			return
		}
		if content, exists := s.Attr("content"); exists && magentoMetaPattern.MatchString(content) {
			found = true
		}
	})
	return found
}

func checkMagentoGraphQL(client *http.Client, baseURL string) bool {
	if client == nil || baseURL == "" {
		return false
	}

	graphqlURL := strings.TrimRight(baseURL, "/") + graphQLPath + "?query=" + url.QueryEscape(graphQLQuery)

	resp, err := client.Get(graphqlURL) //nolint:noctx
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	bodyStr := string(body)
	return strings.Contains(bodyStr, "The current customer") && strings.Contains(bodyStr, "graphql-authorization")
}
