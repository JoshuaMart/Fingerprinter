package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/JoshuaMart/fingerprinter/internal/config"
	"github.com/JoshuaMart/fingerprinter/internal/scanner"
	"github.com/JoshuaMart/fingerprinter/internal/server"
	"github.com/JoshuaMart/fingerprinter/internal/worker"
)

var version = "dev"

func main() {
	var (
		configPath   string
		portOverride int
		showVersion  bool
		mode         string
	)

	flag.StringVar(&configPath, "config", "", "path to configuration file")
	flag.IntVar(&portOverride, "port", 0, "override server port")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&mode, "mode", "api", "run mode: api or worker")
	flag.Parse()

	if showVersion {
		fmt.Println("fingerprinter", version)
		os.Exit(0)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Configure log level from config (YAML + env override)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()})))

	if portOverride > 0 {
		cfg.Server.Port = portOverride
	}

	scn, err := scanner.New(cfg)
	if err != nil {
		slog.Error("failed to initialize scanner", "error", err)
		os.Exit(1)
	}
	defer scn.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch mode {
	case "api":
		srv := server.New(cfg, scn)
		if err := srv.Run(ctx); err != nil {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}

	case "worker":
		w, err := worker.New(&cfg.Redis, scn)
		if err != nil {
			slog.Error("failed to initialize worker", "error", err)
			os.Exit(1)
		}
		defer func() { _ = w.Close() }()

		// Start health-only HTTP server in background.
		healthSrv := server.NewHealthOnly(cfg)
		go func() {
			if err := healthSrv.Run(ctx); err != nil {
				slog.Error("health server error", "error", err)
			}
		}()

		if err := w.Run(ctx); err != nil {
			slog.Error("worker error", "error", err)
			os.Exit(1)
		}

	default:
		slog.Error("unknown mode", "mode", mode)
		os.Exit(1)
	}
}
