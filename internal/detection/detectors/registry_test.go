package detectors

import "testing"

func TestMagentoRegistered(t *testing.T) {
	all := All()
	found := false
	for _, d := range all {
		if d.Name() == "Magento" {
			found = true
			break
		}
	}
	if !found {
		t.Error("MagentoDetector not found in registry")
	}
}
