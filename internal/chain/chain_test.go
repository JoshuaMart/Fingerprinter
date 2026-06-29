package chain

import (
	"testing"
)

func TestValidateURLValid(t *testing.T) {
	valid := []string{
		"http://example.com",
		"https://example.com",
		"https://example.com:8443/path",
		"http://sub.domain.example.com",
	}
	for _, u := range valid {
		if err := ValidateURL(u); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", u, err)
		}
	}
}

func TestValidateURLBlocked(t *testing.T) {
	blocked := []string{
		"file:///etc/passwd",
		"ftp://example.com",
		"gopher://example.com",
		"",
		"not-a-url",
	}
	for _, u := range blocked {
		if err := ValidateURL(u); err == nil {
			t.Errorf("expected %q to be blocked", u)
		}
	}
}
