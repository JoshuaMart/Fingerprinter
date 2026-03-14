package detectors

import (
	"github.com/JoshuaMart/fingerprinter/internal/detection/detectors/cms"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

// All returns all built-in complex detectors.
func All() []models.Detector {
	return []models.Detector{
		&cms.MagentoDetector{},
	}
}
