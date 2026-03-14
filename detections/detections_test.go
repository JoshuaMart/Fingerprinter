package detections

import (
	"net/http"
	"testing"

	yamldet "github.com/JoshuaMart/fingerprinter/internal/detection/yaml"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

func TestLoadRealDetections(t *testing.T) {
	defs, err := yamldet.LoadDir(".")
	if err != nil {
		t.Fatalf("failed to load detections: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("expected at least one detection file")
	}
}

func TestPHPDetection(t *testing.T) {
	defs, err := yamldet.LoadDir(".")
	if err != nil {
		t.Fatalf("failed to load detections: %v", err)
	}

	var phpDef *yamldet.Definition
	for i := range defs {
		if defs[i].Name == "PHP" {
			phpDef = &defs[i]
			break
		}
	}
	if phpDef == nil {
		t.Fatal("PHP detection not found")
	}

	det := yamldet.NewDetector(*phpDef)

	ctx := &models.DetectionContext{
		Responses: []models.ChainedResponse{
			{
				RawHeaders: http.Header{
					"X-Powered-By": {"PHP/8.2.3"},
					"Set-Cookie":   {"PHPSESSID=abc123; Path=/"},
				},
			},
		},
	}

	res, err := det.Detect(ctx)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !res.Detected {
		t.Fatal("expected PHP to be detected")
	}
	if res.Version != "8.2.3" {
		t.Errorf("expected version '8.2.3', got %q", res.Version)
	}
}
