package cms

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

func TestMagentoHeaders(t *testing.T) {
	det := &MagentoDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"X-Magento-Debug":         {"1"},
					"X-Magento-Cache-Control": {"max-age=86400"},
				},
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Magento detected via headers")
	}
}

func TestMagentoBody(t *testing.T) {
	det := &MagentoDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				Body: []byte(`<script>require(['Magento_PageCache/js/page-cache'])</script>
				<script>Mage.Cookies.path = '/';</script>`),
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Magento detected via body patterns")
	}
}

func TestMagentoMeta(t *testing.T) {
	det := &MagentoDetector{}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><meta name="generator" content="Magento 2"></head></html>`,
	))

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{{}},
		Document:  doc,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Magento detected via meta tag")
	}
}

func TestMagentoGraphQL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/graphql") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"errors":[{"message":"The current customer isn't authorized.","extensions":{"category":"graphql-authorization"}}]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := &MagentoDetector{}
	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{{}},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Magento detected via GraphQL endpoint")
	}
}

func TestMagentoGraphQLNotMagento(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	det := &MagentoDetector{}
	ctx := &models.DetectionContext{
		Responses:  []models.ChainedResponse{{}},
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no detection when GraphQL returns 404 and no other signals")
	}
}

func TestMagentoNotDetected(t *testing.T) {
	det := &MagentoDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{"Server": {"nginx"}},
				Body:       []byte("<html><body>Hello</body></html>"),
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.Detected {
		t.Error("expected no Magento detection")
	}
}

func TestMagentoCombined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/graphql") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"errors":[{"message":"The current customer isn't authorized.","extensions":{"category":"graphql-authorization"}}]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><meta name="generator" content="Magento 2"></head></html>`,
	))

	det := &MagentoDetector{}
	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{"X-Magento-Debug": {"1"}},
				Body:       []byte(`<script>require(['Magento_PageCache'])</script>`),
			},
		},
		Document:   doc,
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected Magento detected")
	}
	if !res.Detected {
		t.Error("expected detection with combined signals")
	}
}
