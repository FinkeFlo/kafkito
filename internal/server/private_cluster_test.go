// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
	"github.com/go-chi/chi/v5"
)

func encodeHeader(t *testing.T, cfg config.ClusterConfig) string {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestDecodePrivateClusterHeader(t *testing.T) {
	good := config.ClusterConfig{
		Name:    "mine",
		Brokers: []string{"localhost:9092"},
		Auth:    config.AuthConfig{Type: "none"},
	}
	got, err := decodePrivateClusterHeader(encodeHeader(t, good))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Brokers) != 1 || got.Brokers[0] != "localhost:9092" {
		t.Fatalf("brokers = %v", got.Brokers)
	}

	cases := map[string]string{
		"not-base64":   "!!!",
		"bad-json":     base64.StdEncoding.EncodeToString([]byte("{not json")),
		"no-brokers":   base64.StdEncoding.EncodeToString([]byte(`{"name":"x"}`)),
		"empty-broker": base64.StdEncoding.EncodeToString([]byte(`{"name":"x","brokers":[""]}`)),
		"bad-auth":     base64.StdEncoding.EncodeToString([]byte(`{"name":"x","brokers":["a:1"],"auth":{"type":"plain"}}`)),
	}
	for label, raw := range cases {
		if _, err := decodePrivateClusterHeader(raw); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestPrivateClusterMiddlewareStoresContext(t *testing.T) {
	cfg := config.ClusterConfig{Name: "x", Brokers: []string{"a:1"}}
	var seen bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok := privateClusterFromContext(r.Context())
		if !ok {
			t.Errorf("ctx did not carry config")
			return
		}
		if got.Brokers[0] != "a:1" {
			t.Errorf("brokers = %v", got.Brokers)
		}
		seen = true
	})
	h := privateClusterMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !seen {
		t.Fatal("handler not called")
	}
}

func TestPrivateClusterMiddlewareRejectsBadHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true })
	h := privateClusterMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(PrivateClusterHeader, "!!!not-base64!!!")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatal("handler should not run on malformed header")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRegistryUseAdhocReusesFingerprint(t *testing.T) {
	reg := kafkapkg.NewRegistry(nil, slog.Default())
	cfg := config.ClusterConfig{
		Name:    "ignored",
		Brokers: []string{"b:1", "a:0"},
		Auth:    config.AuthConfig{Type: "none"},
	}
	n1, err := reg.UseAdhoc(cfg)
	if err != nil {
		t.Fatalf("use1: %v", err)
	}
	if !strings.HasPrefix(n1, kafkapkg.AdhocPrefix) {
		t.Fatalf("name = %q, want adhoc prefix", n1)
	}

	cfg.Name = "different-display-name"
	n2, err := reg.UseAdhoc(cfg)
	if err != nil {
		t.Fatalf("use2: %v", err)
	}
	if n1 != n2 {
		t.Errorf("same config produced different names: %q vs %q", n1, n2)
	}

	cfg.Brokers = []string{"other:9092"}
	n3, err := reg.UseAdhoc(cfg)
	if err != nil {
		t.Fatalf("use3: %v", err)
	}
	if n3 == n1 {
		t.Error("different brokers should yield different fingerprint")
	}
}

func TestResolvePrivateClusterParamRewritesViaRouter(t *testing.T) {
	reg := kafkapkg.NewRegistry(nil, slog.Default())
	cfg := config.ClusterConfig{Brokers: []string{"x:1"}}
	expected, err := reg.UseAdhoc(cfg)
	if err != nil {
		t.Fatalf("adhoc: %v", err)
	}

	var captured string
	r := chi.NewRouter()
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Group(func(g chi.Router) {
			g.Use(privateClusterMiddleware)
			g.Use(resolvePrivateClusterParam(reg))
			g.Get("/clusters/{cluster}/topics", func(_ http.ResponseWriter, req *http.Request) {
				captured = chi.URLParam(req, "cluster")
			})
		})
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+config.PrivateClusterSentinel+"/topics", nil)
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if captured != expected {
		t.Errorf("param after rewrite = %q, want %q", captured, expected)
	}
}

func TestResolvePrivateClusterParamRejectsWithoutHeader(t *testing.T) {
	reg := kafkapkg.NewRegistry(nil, slog.Default())
	r := chi.NewRouter()
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Group(func(g chi.Router) {
			g.Use(privateClusterMiddleware)
			g.Use(resolvePrivateClusterParam(reg))
			g.Get("/clusters/{cluster}/topics", func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatal("handler should not run without header")
			})
		})
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+config.PrivateClusterSentinel+"/topics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestConfigValidateRejectsReservedNames(t *testing.T) {
	for _, name := range []string{config.PrivateClusterSentinel, config.AdhocClusterPrefix + "abc"} {
		c := config.Config{Clusters: []config.ClusterConfig{{Name: name, Brokers: []string{"a:1"}}}}
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected reject, got nil", name)
		}
	}
}
