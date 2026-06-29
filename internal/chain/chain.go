package chain

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateURL checks that the URL scheme is HTTP(S) and the hostname is not empty.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("blocked scheme %q: only http and https are allowed", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("empty hostname")
	}
	return nil
}
