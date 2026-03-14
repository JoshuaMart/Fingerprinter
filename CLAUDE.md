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
  chain/                 HTTP redirect chain follower + DOM parser
  browser/               Rod browser pool (headless Chromium), hijack routing, JS eval
  detection/
    yaml/                YAML-based detector loader, schema, favicon hashing
    engine/              Parallel detection engine (runs all detectors concurrently)
    detectors/           Complex Go detectors (implement models.Detector interface)
      cms/               CMS-specific detectors (e.g. Magento)
  httpclient/            HTTP client factory (timeout, proxy)
  metadata/              robots.txt, sitemap, favicon metadata fetcher
  models/                Shared types (ScanRequest, ScanResult, DetectionContext, etc.)
  config/                YAML config loader
detections/              YAML detection files organized by category (cms/, js-libraries/, languages/)
```

## Scan pipeline (scanner.go)

1. **HTTP chain** — Follow redirects (max configurable hops), capture each hop
2. **DOM parsing** — Parse final response with goquery
3. **Browser** (optional) — Rod opens page, monitors external hosts via request hijacking
4. **404 probe** — Request random path to capture error page for detection
5. **Detections** — Engine runs all YAML + Go detectors in parallel against DetectionContext
6. **Metadata** — Fetch robots.txt, sitemap, favicon
7. **Aggregation** — Assemble final ScanResult JSON

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
  paths:               # Probe specific paths (status code check)
  js:                  # Evaluate JS in browser context (requires browser_detection)
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

## Key dependencies

- `go-chi/chi/v5` — HTTP routing
- `go-rod/rod` — Headless browser (Chromium)
- `PuerkitoBio/goquery` — DOM parsing
- `twmb/murmur3` — Favicon hash (Shodan-compatible mmh3)
- `gopkg.in/yaml.v3` — YAML config + detection loading
