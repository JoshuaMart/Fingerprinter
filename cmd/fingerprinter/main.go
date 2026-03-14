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
)

var version = "dev"

func main() {
	var (
		configPath   string
		portOverride int
		showVersion  bool
	)

	flag.StringVar(&configPath, "config", "", "path to configuration file")
	flag.IntVar(&portOverride, "port", 0, "override server port")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
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

	srv := server.New(cfg, scn)
	if err := srv.Run(ctx); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
