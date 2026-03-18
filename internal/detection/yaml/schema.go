package yaml

// Definition represents a YAML detection file.
type Definition struct {
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
	Website  string `yaml:"website"`
	Checks   Checks `yaml:"checks"`
}

// Checks holds all check types for a detection.
type Checks struct {
	Headers     map[string]HeaderCheck `yaml:"headers"`
	Body        []BodyCheck            `yaml:"body"`
	Meta        map[string]MetaCheck   `yaml:"meta"`
	Cookies     map[string]CookieCheck `yaml:"cookies"`
	Paths       []PathCheck            `yaml:"paths"`
	JS          []JSCheck              `yaml:"js"`
	FaviconHash []int32                `yaml:"favicon_hash"`
}

// HeaderCheck matches a regex against an HTTP header value.
type HeaderCheck struct {
	Pattern string `yaml:"pattern"`
	Version string `yaml:"version"`
}

// BodyCheck matches a regex against the response body.
type BodyCheck struct {
	Pattern string `yaml:"pattern"`
	Version string `yaml:"version"`
}

// MetaCheck matches a regex against a meta tag content.
type MetaCheck struct {
	Pattern string `yaml:"pattern"`
	Version string `yaml:"version"`
}

// CookieCheck matches a regex against a cookie value.
type CookieCheck struct {
	Pattern string `yaml:"pattern"`
}

// PathCheck sends a request to a path and checks the status code.
// By default, uses HTTP client. Set Browser: true to use the browser (needed for JS eval on path page).
type PathCheck struct {
	Path    string `yaml:"path"`
	Status  int    `yaml:"status"`
	Browser bool   `yaml:"browser"`
}

// JSCheck evaluates a JS expression in the browser context.
type JSCheck struct {
	Expression string `yaml:"expression"`
	Version    bool   `yaml:"version"`
}
