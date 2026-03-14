# Fingerprinter

Open-source web technology detection engine with a built-in REST API.

Fingerprinter scans a URL, follows redirects, analyzes HTTP headers, HTML body, cookies, meta tags, JavaScript globals, and returns a list of detected technologies with version information.

## Features

- HTTP redirect chain following with hop-by-hop capture
- YAML-based detection system (headers, body, meta, cookies, paths, JS)
- Complex Go-based detectors for advanced detection logic
- Headless browser support via Rod (JS evaluation, screenshots)
- Concurrent scan limiting via semaphore

## Installation

### Binary

```bash
# Build for current platform
make build

# Cross-compile (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64)
make build-all

# Run
./bin/fingerprinter --config config.yml
```

### Docker

```bash
docker compose up -d

# Or manually
docker build -t fingerprinter .
docker run -p 3001:3001 -v ./config.yml:/app/config.yml fingerprinter
```

### CLI Flags

| Flag | Description | Default |
|---|---|---|
| `--config` | Path to YAML config file | (none, uses defaults) |
| `--port` | Override server port | (from config) |
| `--version` | Print version and exit | |

## Configuration

Copy `config.example.yml` to `config.yml` and edit as needed:

```yaml
server:
  port: 3001
  read_timeout: 30s

scanner:
  max_redirects: 10
  request_timeout: 10s
  headers:
    User-Agent: "Fingerprinter/1.0"
  concurrent_scans: 50
  # proxy: "http://127.0.0.1:8080"

browser:
  enabled: true
  pool_size: 5
  page_timeout: 15s

detections:
  yaml_dir: "./detections/"

```

### Environment Variables

All configuration values can be overridden with environment variables:

| Variable | Description |
|---|---|
| `FINGERPRINTER_SERVER_PORT` | HTTP server port |
| `FINGERPRINTER_SCANNER_USER_AGENT` | User-Agent header |
| `FINGERPRINTER_BROWSER_ENABLED` | Enable headless browser (`true`/`false`) |
| `FINGERPRINTER_DETECTIONS_YAML_DIR` | Path to YAML detections directory |
| `FINGERPRINTER_SCANNER_PROXY` | HTTP proxy URL (e.g. `http://127.0.0.1:8080`) |

## API

### `POST /scan`

Scan a URL for technologies.

```bash
curl -X POST http://localhost:3001/scan \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com",
    "options": {
      "browser_detection": true,
      "timeout_seconds": 30,
      "max_redirects": 10
    }
  }'
```

Response:

```json
{
  "url": "https://example.com",
  "chain": [
    {
      "url": "https://example.com",
      "status_code": 200,
      "headers": { "Content-Type": "text/html" },
      "title": "Example Domain",
      "response_size": 1256
    }
  ],
  "technologies": [
    {
      "name": "PHP",
      "version": "8.2.3",
      "category": "Language"
    }
  ],
  "cookies": {
    "PHPSESSID": "abc123"
  },
  "metadata": {
    "robots_txt": true,
    "sitemap": "https://example.com/sitemap.xml",
    "favicon": "https://example.com/favicon.ico"
  },
  "scanned_at": "2026-03-13T12:00:00Z"
}
```

### `GET /health`

```bash
curl http://localhost:3001/health
```

```json
{ "status": "ok", "version": "v0.1.0" }
```

### `GET /detections`

List all loaded detections.

```bash
curl http://localhost:3001/detections
```

```json
{
  "detections": [
    { "name": "PHP", "category": "Language" },
    { "name": "jQuery", "category": "JS Library" },
    { "name": "Magento", "category": "E-commerce" }
  ]
}
```

## Writing Detections

### YAML Detections

Add `.yml` files to the `detections/` directory (subdirectories are supported). All files are loaded recursively at startup.

```
detections/
  languages/
    php.yml
  js-libraries/
    jquery.yml
```

#### Format

Use single quotes for regex patterns to avoid YAML escaping issues.

```yaml
name: Technology Name
category: Category        # e.g. Language, CMS, Framework, CDN, Analytics...
website: https://example.com
checks:
  headers:
    header-name:
      pattern: 'regex'
      version: '(capture group)'   # optional, applied on the same value as pattern

  body:
    - pattern: 'regex in body'
      version: '(\d+\.\d+)'       # optional

  meta:
    meta-name:                     # matches <meta name="..." content="...">
      pattern: 'regex'
      version: '(\d+)'            # optional

  cookies:
    cookie_name:                   # presence check only (no pattern = just check existence)
    another_cookie:
      pattern: 'regex on value'    # optional value match

  paths:
    - path: '/specific-path'
      status: 200                  # expected HTTP status code

  js:
    - expression: 'window.jQuery'  # JS expression evaluated in browser context
      version: false               # if true, the expression return value is used as version

  favicon_hash:                    # Shodan-compatible mmh3 hash of the favicon
    - 1099097618
```

#### Check Types

| Check | Description | Version extraction |
|---|---|---|
| `headers` | Regex on HTTP response header value | `version` regex on same value |
| `body` | Regex on response body | `version` regex on same body match |
| `meta` | Regex on `<meta>` tag content attribute | `version` regex on content value |
| `cookies` | Cookie name existence, optional value regex | No |
| `paths` | HTTP request to path, check status code | No |
| `js` | JS expression in browser (requires `browser_detection: true`) | If `version: true`, expression result is the version |
| `favicon_hash` | Shodan-compatible mmh3 hash of the site favicon | No |

#### Example: PHP

```yaml
name: PHP
category: Language
website: https://www.php.net
checks:
  headers:
    x-powered-by:
      pattern: 'PHP'
      version: '(\d+\.\d+\.\d+)'
  cookies:
    PHPSESSID:
```

### Complex Go Detectors

For detection logic that goes beyond pattern matching (e.g. probing API endpoints, conditional checks), implement the `Detector` interface in Go.

```go
type Detector interface {
    Name() string
    Category() string
    Detect(ctx *DetectionContext) (*DetectionResult, error)
}
```

#### DetectionContext

The `Detect` method receives a context with all available data:

```go
type DetectionContext struct {
    Responses   []ChainedResponse  // All hops in the redirect chain
    Document    *goquery.Document  // Parsed DOM of the final response
    HTTPClient  *http.Client       // HTTP client for additional requests
    Browser     *rod.Browser       // Rod browser instance (nil if disabled)
    BrowserPage *rod.Page          // Current page (nil if disabled)
    BaseURL     string             // Final URL after redirects
}
```

#### DetectionResult

```go
type DetectionResult struct {
    Detected bool   // Was the technology found?
    Version  string // Detected version (optional)
    Evidence string // Human-readable evidence (optional)
}
```

#### Steps

1. Create a file in `internal/detection/detectors/<category>/` (e.g. `cms/shopify.go`)
2. Implement the `Detector` interface
3. Register it in `internal/detection/detectors/registry.go`:

```go
import (
    "github.com/JoshuaMart/fingerprinter/internal/detection/detectors/cms"
    "github.com/JoshuaMart/fingerprinter/internal/models"
)

func All() []models.Detector {
    return []models.Detector{
        &cms.MagentoDetector{},
        &cms.ShopifyDetector{}, // add here
    }
}
```

## Development

```bash
make test       # Run tests with race detector
make lint       # Run golangci-lint
make build      # Build for current platform
make build-all  # Cross-compile all platforms
make docker     # Build Docker image
```

## License

[MIT](LICENSE)
