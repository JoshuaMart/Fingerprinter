package engine

import (
	"net/http"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"

	"github.com/JoshuaMart/fingerprinter/internal/models"
)

// Engine runs all registered detectors and aggregates results.
type Engine struct {
	mu        sync.RWMutex
	detectors []models.Detector
}

// New creates a new detection engine.
func New() *Engine {
	return &Engine{}
}

// Register adds a detector to the engine.
func (e *Engine) Register(d models.Detector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.detectors = append(e.detectors, d)
}

// Detectors returns the list of registered detectors.
func (e *Engine) Detectors() []models.Detector {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.detectors
}

// BuildContext creates a DetectionContext from chain results and parsed DOM.
func BuildContext(responses []models.ChainedResponse, doc *goquery.Document, httpClient *http.Client, browser *rod.Browser, baseURL string) *models.DetectionContext {
	return &models.DetectionContext{
		Responses:  responses,
		Document:   doc,
		HTTPClient: httpClient,
		Browser:    browser,
		BaseURL:    baseURL,
	}
}

type detectionEntry struct {
	name     string
	category string
	result   *models.DetectionResult
}

// Run executes all registered detectors in parallel and returns aggregated technologies.
func (e *Engine) Run(ctx *models.DetectionContext) []models.Technology {
	e.mu.RLock()
	detectors := make([]models.Detector, len(e.detectors))
	copy(detectors, e.detectors)
	e.mu.RUnlock()

	results := make(chan detectionEntry, len(detectors))
	var wg sync.WaitGroup

	for _, d := range detectors {
		wg.Add(1)
		go func(det models.Detector) {
			defer wg.Done()
			res, err := det.Detect(ctx)
			if err != nil || res == nil || !res.Detected {
				return
			}
			results <- detectionEntry{
				name:     det.Name(),
				category: det.Category(),
				result:   res,
			}
		}(d)
	}

	wg.Wait()
	close(results)

	return aggregate(results)
}

// aggregate deduplicates detections by name.
func aggregate(results chan detectionEntry) []models.Technology {
	seen := make(map[string]models.Technology)

	for entry := range results {
		if _, ok := seen[entry.name]; ok {
			continue
		}
		seen[entry.name] = models.Technology{
			Name:     entry.name,
			Category: entry.category,
			Version:  entry.result.Version,
		}
	}

	techs := make([]models.Technology, 0, len(seen))
	for _, t := range seen {
		techs = append(techs, t)
	}
	return techs
}
