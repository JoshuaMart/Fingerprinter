FROM golang:1.26.1-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

ARG VERSION=dev

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o fingerprinter ./cmd/fingerprinter

# ---

FROM alpine:3.23

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /build/fingerprinter .
COPY --from=builder /build/detections/ ./detections/

EXPOSE 3001

ENTRYPOINT ["./fingerprinter"]
CMD ["--config", "config.yml"]
