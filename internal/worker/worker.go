package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	cfg      *config.RedisConfig
	scanner  Scanner
	client   *redis.Client
	consumer string
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

	consumer := cfg.Consumer
	if consumer == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("resolving hostname for consumer name: %w", err)
		}
		consumer = h
	}

	return &Worker{
		cfg:      cfg,
		scanner:  scanner,
		client:   redis.NewClient(opts),
		consumer: consumer,
	}, nil
}

// Run starts the worker loop. It blocks until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}

	// Create consumer group (idempotent).
	err := w.client.XGroupCreateMkStream(ctx, w.cfg.Stream, w.cfg.Group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("creating consumer group: %w", err)
	}

	slog.Info("worker started",
		"stream", w.cfg.Stream,
		"group", w.cfg.Group,
		"consumer", w.consumer,
	)

	for {
		streams, err := w.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    w.cfg.Group,
			Consumer: w.consumer,
			Streams:  []string{w.cfg.Stream, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err == redis.Nil {
				continue
			}
			slog.Error("xreadgroup error", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				w.processMessage(ctx, msg)

				if err := w.client.XAck(ctx, w.cfg.Stream, w.cfg.Group, msg.ID).Err(); err != nil {
					slog.Error("xack failed", "id", msg.ID, "error", err)
				}
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
		// TODO: emit profile:ready / technology:detected events on Redis

	case "transport:response":
		slog.Info("transport:response not implemented yet, skipping", "id", msg.ID)

	default:
		slog.Warn("unknown message type, skipping", "id", msg.ID, "type", msgType)
	}
}

// Close shuts down the Redis client.
func (w *Worker) Close() error {
	return w.client.Close()
}
