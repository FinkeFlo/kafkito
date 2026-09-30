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
	"strings"
	"syscall"
	"time"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/server"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	configPath := flag.String("config", "", "path to YAML config file (overrides KAFKITO_CONFIG)")
	flag.Parse()
	os.Exit(run(*configPath))
}

// run starts the server and blocks until shutdown. It returns the process exit
// code instead of calling os.Exit itself, so deferred cleanup always runs.
func run(configPath string) int {
	// Bootstrap logger until the config (which carries the log settings) is
	// loaded; config-load errors are reported through it.
	bootstrap := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(bootstrap)

	cfg, err := config.Load(configPath)
	if err != nil {
		bootstrap.Error("config load failed", "err", err)
		return 2
	}

	logger := newLogger(cfg.Log)
	slog.SetDefault(logger)
	logger.Info("config loaded",
		"addr", cfg.Server.Addr,
		"clusters", len(cfg.Clusters),
		"log_level", strings.ToLower(cfg.Log.SlogLevel().String()),
		"log_format", cfg.Log.FormatName(),
	)

	registry := kafkapkg.NewRegistry(cfg.Clusters, logger)
	defer registry.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the background metrics collector. Uses the process context so
	// it's cancelled on shutdown; registry.Close() also waits for it.
	registry.StartMetrics(ctx, 0)

	// config.Load already applied KAFKITO_AUTH_MODE and the "off" default.
	mode := cfg.Auth.Mode

	modeCfg := authModeConfig(cfg.Auth)
	populateAuthConfigFromEnv(&modeCfg)
	validator, cleanup, err := auth.BuildValidator(modeCfg)
	if err != nil {
		logger.Error("auth init failed", "mode", mode, "err", err)
		return 2
	}
	// BuildValidator never returns a nil cleanup on success; the check keeps
	// shutdown from panicking if that contract ever regresses (v1.2.0 did).
	if cleanup != nil {
		defer cleanup()
	}
	authAttrs := []any{"mode", mode}
	if ov, ok := validator.(*auth.OIDCValidator); ok && mode == config.AuthModeOIDC {
		oc := ov.Config()
		authAttrs = append(authAttrs, "issuer", oc.IssuerURL, "audience", oc.Audience, "jwks_url", oc.JWKSEndpoint)
	}
	logger.Info("auth initialised", authAttrs...)

	// config.Load already applied $PORT and the default address.
	addr := cfg.Server.Addr
	// VCAP_APPLICATION and KAFKITO_INSECURE_AUTH_OFF are platform and guard
	// variables, not config settings, so the guard reads them from the environment.
	if err := guardAuthMode(mode, addr, os.Getenv); err != nil {
		logger.Error("insecure auth configuration", "mode", mode, "addr", addr, "err", err)
		return 2
	}

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
		return 1
	}
	logger.Info("kafkito stopped")
	return 0
}

// authModeConfig maps the loaded auth settings onto the auth package's
// ModeConfig. Build-specific settings (VCAP_SERVICES) are added by
// populateAuthConfigFromEnv.
func authModeConfig(a config.AppAuthConfig) auth.ModeConfig {
	return auth.ModeConfig{
		Mode: a.Mode,
		OIDC: auth.OIDCConfig{
			IssuerURL:    a.OIDC.IssuerURL,
			Audience:     a.OIDC.Audience,
			JWKSEndpoint: a.OIDC.JWKSURL,
			RequiredTyp:  a.OIDC.RequiredTyp,
			AllowedAZP:   a.OIDC.AllowedAZP,
		},
	}
}

// newLogger builds the process logger on stdout from the log config.
func newLogger(c config.LogConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.SlogLevel()}
	if c.FormatName() == config.LogFormatText {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
