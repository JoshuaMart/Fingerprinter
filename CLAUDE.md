# Fingerprinter Core

Web technology fingerprinting engine with built-in REST API. Stateless, no database. Written in Go.

## Quick reference

```bash
make build          # Build binary to bin/fingerprinter
make test           # go test -race -cover ./...
make lint           # golangci-lint run ./...
make run            # Build + run with config.example.yml
make docker-run     # docker compose up -d
```

## Project structure

```
cmd/fingerprinter/       Entry point (main.go)
internal/
  server/                Chi HTTP server (POST /scan, GET /health, GET /detections)
  scanner/               Scan pipeline orchestrator
  chain/                 URL validation, DOM parser, cookie/header helpers
  browser/               Rod browser pool (remote Chrome via CDP), network capture, JS eval
    network.go           CDP Network event capture (redirect chain, external hosts)
    pool.go              Page pool, Navigate, NavigateAndCapture
  detection/
    yaml/                YAML-based detector loader, schema, favicon hashing
    engine/              Parallel detection engine (runs all detectors concurrently)
    detectors/           Complex Go detectors (implement models.Detector interface)
      cms/               CMS-specific detectors (e.g. Magento)
  httpclient/            HTTP client factory (timeout, proxy)
  metadata/              robots.txt, sitemap, favicon metadata fetcher
  models/                Shared types (ScanRequest, ScanResult, DetectionContext, BrowserNavigator, etc.)
  config/                YAML config loader
detections/              YAML detection files organized by category (cms/, js-libraries/, languages/)
```

## Architecture — Browser-only pipeline

All navigation goes through a remote Chrome browser (headless, Docker). There is no HTTP chain follower — the browser captures the redirect chain via CDP Network events.

**Chrome** (via `chromedp/headless-shell`) runs as a separate Docker service exposing CDP on port 9222. Rod connects to it via `control_url` (HTTP, resolved to WebSocket).

The HTTP client is kept only for: Go detector probes (e.g. Magento GraphQL), metadata (robots.txt, sitemap), and favicon byte fetching for mmh3 hash.

## Scan pipeline (scanner.go)

1. **Browser navigate** — Rod opens URL in Chrome, captures redirect chain + external hosts via CDP Network events
2. **DOM parsing** — Parse rendered HTML (post-JS) with goquery
3. **404 probe** — Navigate to random path via browser to capture error page
4. **Detections** — Engine runs all YAML + Go detectors in parallel against DetectionContext
5. **Metadata** — Fetch robots.txt, sitemap, favicon via HTTP
6. **Aggregation** — Assemble final ScanResult JSON

## Adding detections

### YAML detections (no recompile)

Add `.yml` files to `detections/` subdirectories. Schema:

```yaml
name: TechName
category: CMS          # See SPECIFICATIONS.md §4.3.1 for standard categories
website: https://...
checks:
  headers:             # Match response headers
  body:                # Match response body patterns
  meta:                # Match HTML meta tags
  cookies:             # Match cookie names
  paths:               # Probe specific paths via browser (status code check)
  js:                  # Evaluate JS in browser context
  favicon:             # Match favicon mmh3 hash (Shodan-compatible)
```

### Go detectors (compiled)

Implement `models.Detector` interface in `internal/detection/detectors/`. Register in `registry.go` via `All()`.

## CI

GitHub Actions runs on push/PR to main:
- **lint**: golangci-lint v2 (gofmt, errcheck, govet, staticcheck, unused, ineffassign, misspell)
- **test**: `go test -race -cover ./...`

## Code style

- Format with `gofmt` (enforced by CI)
- Struct field alignment must pass gofmt (watch composite literals too)
- US English for identifiers (enforced by misspell linter)
- Comments and docs are in English; SPECIFICATIONS.md is in French

## Configuration

See `config.example.yml`. Config loaded from `--config` flag. Key sections: `server`, `scanner`, `browser`, `detections`. LLM section (`llm`) is planned but not yet implemented.

Browser `control_url` defaults to `http://localhost:9222`. Override with env var `FINGERPRINTER_BROWSER_CONTROL_URL`.

## Key dependencies

- `go-chi/chi/v5` — HTTP routing
- `go-rod/rod` — Browser automation (connects to remote Chrome via CDP)
- `PuerkitoBio/goquery` — DOM parsing
- `twmb/murmur3` — Favicon hash (Shodan-compatible mmh3)
- `gopkg.in/yaml.v3` — YAML config + detection loading

## Running with Docker

```bash
docker compose up -d   # Starts Chrome + Fingerprinter core
```

Chrome runs as a sidecar container. The core service connects to it via `http://chrome:9222`.
