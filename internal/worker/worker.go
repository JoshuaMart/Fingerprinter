package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/JoshuaMart/fingerprinter/internal/config"
	"github.com/JoshuaMart/fingerprinter/internal/models"
)

// Scanner is the interface the worker needs to launch scans.
type Scanner interface {
	Scan(ctx context.Context, req models.ScanRequest) (*models.ScanResult, error)
}

// Worker consumes scan jobs from a Redis Stream.
type Worker struct {
	cfg     *config.RedisConfig
	scanner Scanner
	client  *redis.Client
	lastID  string
}

// New creates a new Worker. Returns an error if the Redis config is invalid.
func New(cfg *config.RedisConfig, scanner Scanner) (*Worker, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("redis.url is required in worker mode")
	}

	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing redis URL: %w", err)
	}

	return &Worker{
		cfg:     cfg,
		scanner: scanner,
		client:  redis.NewClient(opts),
		lastID:  "$",
	}, nil
}

// Run starts the worker loop. It blocks until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}

	slog.Info("worker started", "stream", w.cfg.Stream)

	for {
		streams, err := w.client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{w.cfg.Stream, w.lastID},
			Count:   1,
			Block:   5 * time.Second,
		}).Result()

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err == redis.Nil {
				continue
			}
			slog.Error("xread error", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				w.lastID = msg.ID
				w.processMessage(ctx, msg)
			}
		}
	}
}

func (w *Worker) processMessage(ctx context.Context, msg redis.XMessage) {
	msgType, _ := msg.Values["type"].(string)
	scanID, _ := msg.Values["scan_id"].(string)
	target, _ := msg.Values["target"].(string)

	slog.Info("processing message", "id", msg.ID, "type", msgType, "scan_id", scanID, "target", target)

	switch msgType {
	case "scan:requested", "endpoint:detected":
		if target == "" {
			slog.Warn("message has no target, skipping", "id", msg.ID)
			return
		}

		req := models.ScanRequest{
			URL:     target,
			Options: &models.ScanOptions{},
		}

		result, err := w.scanner.Scan(ctx, req)
		if err != nil {
			slog.Error("scan failed", "id", msg.ID, "scan_id", scanID, "target", target, "error", err)
			return
		}

		slog.Info("scan completed",
			"id", msg.ID,
			"scan_id", scanID,
			"target", target,
			"technologies", len(result.Technologies),
		)

		w.emitEvents(ctx, scanID, target, result)

	case "profile:ready", "technology:detected":
		// Emitted by this worker — ignore.

	case "transport:response":
		slog.Info("transport:response not implemented yet, skipping", "id", msg.ID)

	default:
		slog.Warn("unknown message type, skipping", "id", msg.ID, "type", msgType)
	}
}

func (w *Worker) emitEvents(ctx context.Context, scanID, target string, result *models.ScanResult) {
	for _, tech := range result.Technologies {
		err := w.client.XAdd(ctx, &redis.XAddArgs{
			Stream: w.cfg.Stream,
			Values: map[string]interface{}{
				"type":     "technology:detected",
				"scan_id":  scanID,
				"target":   target,
				"name":     tech.Name,
				"version":  tech.Version,
				"category": tech.Category,
			},
		}).Err()
		if err != nil {
			slog.Error("failed to emit technology:detected", "scan_id", scanID, "name", tech.Name, "error", err)
		}
	}

	payload, err := json.Marshal(result)
	if err != nil {
		slog.Error("failed to marshal scan result", "scan_id", scanID, "error", err)
		return
	}

	err = w.client.XAdd(ctx, &redis.XAddArgs{
		Stream: w.cfg.Stream,
		Values: map[string]interface{}{
			"type":    "profile:ready",
			"scan_id": scanID,
			"target":  target,
			"result":  string(payload),
		},
	}).Err()
	if err != nil {
		slog.Error("failed to emit profile:ready", "scan_id", scanID, "error", err)
	}
}

// Close shuts down the Redis client.
func (w *Worker) Close() error {
	return w.client.Close()
}
