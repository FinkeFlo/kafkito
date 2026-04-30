// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package config

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestAuthConfigLogValueMasksPassword(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	a := AuthConfig{Type: "plain", Username: "alice", Password: "s3cret"}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "test", slog.Any("auth", a))
	out := buf.String()
	if strings.Contains(out, "s3cret") {
		t.Fatalf("password leaked in log output: %s", out)
	}
	if !strings.Contains(out, `"password":"***"`) {
		t.Fatalf("expected masked password, got: %s", out)
	}
}

func TestSchemaRegistryConfigLogValueMasksPassword(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	s := SchemaRegistryConfig{URL: "https://sr.example", Username: "u", Password: "pw"}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "test", slog.Any("sr", s))
	if strings.Contains(buf.String(), "pw\"") {
		t.Fatalf("password leaked: %s", buf.String())
	}
}

func TestClusterConfigLogValueHidesNested(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	c := ClusterConfig{
		Name:           "prod",
		Brokers:        []string{"b1:9092"},
		Auth:           AuthConfig{Type: "plain", Username: "u", Password: "leaky-password"},
		SchemaRegistry: SchemaRegistryConfig{Password: "another-leaky"},
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "test", slog.Any("cluster", c))
	if strings.Contains(buf.String(), "leaky") {
		t.Fatalf("nested password leaked: %s", buf.String())
	}
}
