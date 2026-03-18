![Image](https://github.com/user-attachments/assets/99b1c5ac-cfc7-4fb7-b250-f76cdd58d991)

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-green"></a>
  <img src="https://img.shields.io/badge/docker-supported-blue?logo=docker">
  <img src="https://img.shields.io/badge/golang-1.26-blue?logo=go">
</p>

Web technology detection engine with a built-in REST API and Redis Stream worker mode.

Scan a URL using a headless browser, capture the full redirect chain via CDP network events, analyze HTTP headers, rendered HTML, cookies, meta tags, JavaScript globals, and return detected technologies with version information.

## Pipeline

```
1. Browser navigate — open URL in Chrome, capture redirect chain + external hosts + WebSockets via CDP
        |
        v
2. DOM parsing — parse rendered HTML (post-JS) with goquery
        |
        v
3. 404 probe — navigate to random path, capture error page response + JS eval
        |
        v
4. Detections — run all YAML + Go detectors in parallel against collected data
        |
        v
5. Metadata — fetch robots.txt, sitemap, favicon via HTTP
        |
        v
6. Aggregation — assemble final JSON response
```

## Quick start

### Docker (recommended)

```bash
docker compose up -d
```

This starts two containers:
- **chrome** — headless Chromium exposing CDP on port 9222
- **core** — Fingerprinter API on port 3001, connected to Chrome

### Binary

Requires a running Chrome (or CDP-compatible) browser accessible via WebSocket.

```bash
# Start Chrome
docker run -d -p 9222:9222 chromedp/headless-shell

# Build and run
make build
./bin/fingerprinter --config config.yml
```

### Worker mode

Run as a Redis Stream consumer instead of an HTTP API. Requires a remote Redis instance.

```bash
./bin/fingerprinter --mode worker --config config.yml
```

The worker consumes messages from the `scans` stream and emits events back on the same stream. A health-only HTTP server (`/health`) is started for k8s probes.

### Usage

```bash
# API mode (default)
curl -X POST http://localhost:3001/scan \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com"}'

# Worker mode — push a scan job to Redis
redis-cli XADD scans '*' type scan:requested scan_id test-123 target https://example.com
```

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
  control_url: "http://localhost:9222"
  pool_size: 5
  page_timeout: 15s

detections:
  yaml_dir: "./detections/"

# redis:                          # required for worker mode only
#   url: "redis://localhost:6379"
#   stream: "scans"
#   group: "fingerprinter"
#   consumer: ""                  # defaults to hostname
```

<details>
<summary>CLI flags</summary>

| Flag | Description | Default |
|---|---|---|
| `--config` | Path to YAML config file | (none, uses defaults) |
| `--port` | Override server port | (from config) |
| `--mode` | Run mode: `api` or `worker` | `api` |
| `--version` | Print version and exit | |

</details>

<details>
<summary>Environment variables</summary>

All configuration values can be overridden with environment variables:

| Variable | Description |
|---|---|
| `FINGERPRINTER_SERVER_PORT` | HTTP server port |
| `FINGERPRINTER_SCANNER_USER_AGENT` | User-Agent header |
| `FINGERPRINTER_BROWSER_CONTROL_URL` | Browser CDP URL (e.g. `http://localhost:9222`) |
| `FINGERPRINTER_DETECTIONS_YAML_DIR` | Path to YAML detections directory |
| `FINGERPRINTER_SCANNER_PROXY` | HTTP proxy URL (e.g. `http://127.0.0.1:8080`) |
| `FINGERPRINTER_REDIS_URL` | Redis URL (e.g. `redis://localhost:6379`) |
| `FINGERPRINTER_REDIS_STREAM` | Redis Stream name (default: `scans`) |
| `FINGERPRINTER_REDIS_GROUP` | Consumer group name (default: `fingerprinter`) |
| `FINGERPRINTER_REDIS_CONSUMER` | Consumer name (default: hostname) |

</details>

## API

### `POST /scan`

Scan a URL for technologies.

```bash
curl -X POST http://localhost:3001/scan \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com",
    "options": {
      "timeout_seconds": 30,
      "max_redirects": 10,
      "skip_path_checks": false
    }
  }'
```

| Option | Type | Default | Description |
|---|---|---|---|
| `timeout_seconds` | int | from config | Scan timeout |
| `max_redirects` | int | from config | Maximum redirects to follow |
| `skip_path_checks` | bool | `false` | Skip all path-based operations (404 probe, path detections, metadata fetch) |

<details>
<summary>Response example</summary>

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
    "llms_txt": false,
    "sitemap": "https://example.com/sitemap.xml",
    "favicon": "https://example.com/favicon.ico"
  },
  "external_hosts": [],
  "web_sockets": [],
  "scanned_at": "2026-03-13T12:00:00Z"
}
```

</details>

### `GET /health`

```bash
curl http://localhost:3001/health
# {"status":"ok","version":"v0.1.0"}
```

### `GET /detections`

List all loaded detections.

```bash
curl http://localhost:3001/detections
# {"detections":[{"name":"PHP","category":"Language"},{"name":"jQuery","category":"JS Library"}]}
```

## Worker Events

In worker mode, Fingerprinter consumes and emits events on a Redis Stream.

### Consumed events

| Type | Fields | Description |
|---|---|---|
| `scan:requested` | `scan_id`, `target` | Triggers a full scan on the target URL |
| `endpoint:detected` | `scan_id`, `target` | Same as `scan:requested` |

### Emitted events

| Type | Fields | Description |
|---|---|---|
| `technology:detected` | `scan_id`, `target`, `name`, `version`, `category` | One event per detected technology |
| `profile:ready` | `scan_id`, `target`, `result` | Full scan result as JSON in `result` field |

## Writing Detections

### YAML Detections

Add `.yml` files to the `detections/` directory (subdirectories are supported). All files are loaded recursively at startup.

```
detections/
  servers/
    nginx.yml
  languages/
    php.yml
  js-libraries/
    jquery.yml
  cms/
    squidex.yml
```

#### Format

Use single quotes for regex patterns to avoid YAML escaping issues.

```yaml
name: Technology Name
category: Category                 # e.g. Language, CMS, Framework, Server, JS Library...
website: https://example.com
checks:
  headers:
    header_name:                   # presence check only (no pattern = just check existence)
    another_header:
      pattern: 'regex'
      version: '(capture group)'   # optional, applied on the same value as pattern

  body:
    matcher: any                   # "any" (default) = at least one match, "all" = all must match
    patterns:
      - pattern: 'regex in body'
        version: '(\d+\.\d+)'     # optional, applied on the full body

  meta:
    meta-name:                     # matches <meta name="..." content="...">
      pattern: 'regex'
      version: '(\d+)'             # optional, applied on the same value as pattern

  cookies:
    cookie_name:                   # presence check only (no pattern = just check existence)
    another_cookie:
      pattern: 'regex on value'    # optional, applied on the same value as pattern

  paths:                           # responses feed into body/headers/cookies/js checks
    - path: '/specific-path'
      status: 200                  # expected HTTP status code
      browser: false               # optional, default false (HTTP client). Set true for browser navigation (needed for JS eval on path page)

  js:
    - expression: 'window.jQuery'  # JS expression evaluated in browser context
      version: false               # if true, the expression return value is used as version

  favicon_hash:                    # mmh3 hash of the favicon
    - 1099097618
```

#### Check Types

| Check | Description | Version extraction |
|---|---|---|
| `headers` | Regex on HTTP response header value (no pattern = presence check) | `version` regex on same value |
| `body` | Regex on response body. `matcher: all` requires all patterns to match (default `any`) | `version` regex on full body |
| `meta` | Regex on `<meta>` tag content attribute | `version` regex on content value |
| `cookies` | Cookie name existence, optional value regex | No |
| `paths` | GET path via HTTP client (default) or browser (`browser: true`), check status code. Response is added to the pool for body/headers/cookies/js checks | No |
| `js` | JS expression evaluated in browser context (main page + path pages) | If `version: true`, expression result is the version |
| `favicon_hash` | mmh3 hash of the site favicon | No |

<details>
<summary>Example: PHP</summary>

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

</details>

<details>
<summary>Example: Swagger UI (path + body + JS)</summary>

```yaml
name: Swagger UI
category: JS Library
website: https://swagger.io/tools/swagger-ui/
checks:
  paths:
    - path: /api/docs
      status: 200
  body:
    patterns:
      - pattern: 'swagger-ui'
  js:
    - expression: "versions['swaggerUI']['version']"
      version: true
```

</details>

### Complex Go Detectors

For detection logic that goes beyond pattern matching (e.g. probing API endpoints, conditional checks), implement the `Detector` interface in Go.

<details>
<summary>Detector interface</summary>

```go
type Detector interface {
    Name() string
    Category() string
    Detect(ctx *DetectionContext) (*DetectionResult, error)
}

type DetectionContext struct {
    Responses   []ChainedResponse      // All hops in the redirect chain
    Document    *goquery.Document      // Parsed rendered DOM (post-JS)
    HTTPClient  *http.Client           // HTTP client for direct requests
    BrowserPool BrowserNavigator       // Navigate to URLs via browser pool
    BrowserPage *rod.Page              // Current page for JS evaluation
    BaseURL        string                 // Final URL after redirects
    SkipPathChecks bool                   // Skip path-based checks (404, paths, metadata)
}

type DetectionResult struct {
    Detected bool   // Was the technology found?
    Version  string // Detected version (optional)
    Evidence string // Human-readable evidence (optional)
}
```

</details>

Steps:

1. Create a file in `internal/detection/detectors/<category>/` (e.g. `cms/shopify.go`)
2. Implement the `Detector` interface
3. Register it in `internal/detection/detectors/registry.go`

## Development

```bash
make test       # Run tests with race detector
make lint       # Run golangci-lint
make build      # Build for current platform
make build-all  # Cross-compile all platforms
make docker     # Build Docker image
```

Browser-dependent tests (browser, scanner, server packages) require a running CDP-compatible instance. Set `FINGERPRINTER_BROWSER_CONTROL_URL` or ensure `http://localhost:9222` is available.

## License

[MIT](LICENSE)
