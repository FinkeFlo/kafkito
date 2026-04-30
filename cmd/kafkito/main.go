// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Command kafkito starts the kafkito HTTP server.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/server"
	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	configPath := flag.String("config", "", "path to YAML config file (overrides KAFKITO_CONFIG)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(2)
	}
	logger.Info("config loaded",
		"addr", cfg.Server.Addr,
		"clusters", len(cfg.Clusters),
	)

	registry := kafkapkg.NewRegistry(cfg.Clusters, logger)
	defer registry.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the background metrics collector. Uses the process context so
	// it's cancelled on shutdown; registry.Close() also waits for it.
	registry.StartMetrics(ctx, 0)

	mode := cfg.Auth.Mode
	if mode == "" {
		mode = os.Getenv("KAFKITO_AUTH_MODE")
	}
	if mode == "" {
		mode = "off"
	}
	// Guard: production binaries must never run "off" mode.
	if mode == "off" && os.Getenv("VCAP_APPLICATION") != "" {
		logger.Error("KAFKITO_AUTH_MODE=off is forbidden when running on Cloud Foundry")
		os.Exit(2)
	}

	modeCfg := auth.ModeConfig{Mode: mode}
	populateAuthConfigFromEnv(&modeCfg)
	validator, cleanup, err := auth.BuildValidator(modeCfg)
	if err != nil {
		logger.Error("auth init failed", "mode", mode, "err", err)
		os.Exit(2)
	}
	defer cleanup()
	logger.Info("auth initialised", "mode", mode)

	addr := listenAddress(cfg.Server.Addr)
	srv := &http.Server{
		Addr: addr,
		Handler: server.New(server.Options{
			Version:  version,
			Logger:   logger,
			Registry: registry,
			Config:   cfg,
			Auth:     validator,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("kafkito starting", "addr", addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("kafkito stopped")
}

// listenAddress returns the HTTP listen address. Honors $PORT (Cloud Foundry /
// Heroku-style) first, then the configured Server.Addr, and finally :37421.
func listenAddress(configured string) string {
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	if configured != "" {
		return configured
	}
	return ":37421"
}
