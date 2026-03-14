package engine

import (
	"errors"
	"testing"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

// mockDetector implements models.Detector for testing.
type mockDetector struct {
	name     string
	category string
	result   *models.DetectionResult
	err      error
}

func (m *mockDetector) Name() string     { return m.name }
func (m *mockDetector) Category() string { return m.category }
func (m *mockDetector) Detect(_ *models.DetectionContext) (*models.DetectionResult, error) {
	return m.result, m.err
}

func TestRegisterAndList(t *testing.T) {
	e := New()
	e.Register(&mockDetector{name: "WordPress", category: "CMS"})
	e.Register(&mockDetector{name: "Nginx", category: "Server"})

	if len(e.Detectors()) != 2 {
		t.Errorf("expected 2 detectors, got %d", len(e.Detectors()))
	}
}

func TestRunSingleDetection(t *testing.T) {
	e := New()
	e.Register(&mockDetector{
		name:     "WordPress",
		category: "CMS",
		result:   &models.DetectionResult{Detected: true, Version: "6.3.1"},
	})

	ctx := &models.DetectionContext{}
	techs := e.Run(ctx)

	if len(techs) != 1 {
		t.Fatalf("expected 1 technology, got %d", len(techs))
	}
	if techs[0].Name != "WordPress" {
		t.Errorf("expected WordPress, got %s", techs[0].Name)
	}
	if techs[0].Version != "6.3.1" {
		t.Errorf("expected version 6.3.1, got %s", techs[0].Version)
	}
}

func TestRunNotDetected(t *testing.T) {
	e := New()
	e.Register(&mockDetector{
		name:     "WordPress",
		category: "CMS",
		result:   &models.DetectionResult{Detected: false},
	})

	ctx := &models.DetectionContext{}
	techs := e.Run(ctx)

	if len(techs) != 0 {
		t.Errorf("expected 0 technologies, got %d", len(techs))
	}
}

func TestRunDetectorError(t *testing.T) {
	e := New()
	e.Register(&mockDetector{
		name:     "Broken",
		category: "CMS",
		result:   nil,
		err:      errMock,
	})
	e.Register(&mockDetector{
		name:     "Nginx",
		category: "Server",
		result:   &models.DetectionResult{Detected: true},
	})

	ctx := &models.DetectionContext{}
	techs := e.Run(ctx)

	if len(techs) != 1 {
		t.Fatalf("expected 1 technology (error detector skipped), got %d", len(techs))
	}
	if techs[0].Name != "Nginx" {
		t.Errorf("expected Nginx, got %s", techs[0].Name)
	}
}

var errMock = errors.New("mock error")

func TestRunDeduplication(t *testing.T) {
	e := New()
	// Two detectors for the same tech — first one wins
	e.Register(&mockDetector{
		name:     "WordPress",
		category: "CMS",
		result:   &models.DetectionResult{Detected: true, Version: "6.3.1"},
	})
	e.Register(&mockDetector{
		name:     "WordPress",
		category: "CMS",
		result:   &models.DetectionResult{Detected: true, Version: ""},
	})

	ctx := &models.DetectionContext{}
	techs := e.Run(ctx)

	if len(techs) != 1 {
		t.Fatalf("expected 1 technology after dedup, got %d", len(techs))
	}
	if techs[0].Name != "WordPress" {
		t.Errorf("expected WordPress, got %s", techs[0].Name)
	}
}

func TestRunMultipleDetections(t *testing.T) {
	e := New()
	e.Register(&mockDetector{
		name:     "WordPress",
		category: "CMS",
		result:   &models.DetectionResult{Detected: true},
	})
	e.Register(&mockDetector{
		name:     "Nginx",
		category: "Server",
		result:   &models.DetectionResult{Detected: true},
	})
	e.Register(&mockDetector{
		name:     "jQuery",
		category: "JS Library",
		result:   &models.DetectionResult{Detected: true, Version: "3.6.0"},
	})

	ctx := &models.DetectionContext{}
	techs := e.Run(ctx)

	if len(techs) != 3 {
		t.Fatalf("expected 3 technologies, got %d", len(techs))
	}

	found := make(map[string]bool)
	for _, tech := range techs {
		found[tech.Name] = true
	}
	for _, name := range []string{"WordPress", "Nginx", "jQuery"} {
		if !found[name] {
			t.Errorf("expected %s in results", name)
		}
	}
}

func TestRunNoDetectors(t *testing.T) {
	e := New()
	ctx := &models.DetectionContext{}
	techs := e.Run(ctx)

	if len(techs) != 0 {
		t.Errorf("expected 0 technologies with no detectors, got %d", len(techs))
	}
}
