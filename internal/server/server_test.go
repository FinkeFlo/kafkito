// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	h := New(Options{Version: "test", Logger: slog.Default()})

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "ok") && !strings.Contains(rec.Body.String(), "ready") {
			t.Errorf("%s: body = %q", path, rec.Body.String())
		}
	}
}

func TestInfoReturnsVersion(t *testing.T) {
	h := New(Options{Version: "1.2.3", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"version":"1.2.3"`) {
		t.Errorf("body missing version: %s", rec.Body.String())
	}
}

func TestAPINotFoundIsJSON(t *testing.T) {
	h := New(Options{Version: "x", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestSPAFallbackServesHTML(t *testing.T) {
	h := New(Options{Version: "x", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// TestSPAFallbackForDeepRoutes covers the exact production reload bug: the
// browser hits a deep client-side path on a fresh GET (e.g. after ⌘R).
// Every such path must render the SPA shell (HTML 200) so TanStack Router
// can take over and resolve the route on the client.
func TestSPAFallbackForDeepRoutes(t *testing.T) {
	h := New(Options{Version: "x", Logger: slog.Default()})
	cases := []string{
		"/topics/foo/messages",
		"/topics/FRA_aspire_eXtend_SalesPrices_PRD/messages",
		"/clusters/PROD",
		"/settings/clusters",
		"/groups",
	}
	for _, p := range cases {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q, want text/html", p, ct)
		}
	}
}

// TestSPAFallbackOnlyAppliesToGetAndHead — non-idempotent verbs against an
// unknown path must NOT receive HTML; they should 404/405 cleanly so
// programmatic clients (curl, scripts) get a sensible error rather than
// the SPA shell.
func TestSPAFallbackOnlyAppliesToGetAndHead(t *testing.T) {
	h := New(Options{Version: "x", Logger: slog.Default()})
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/topics/foo/messages", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s /topics/foo/messages: status = 200, want non-OK", m)
		}
		if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s /topics/foo/messages: Content-Type = %q, want non-HTML", m, ct)
		}
	}
}

// TestBackendPrefixesNeverFallToSPA — known backend prefixes that don't match
// a registered route must 404 with JSON, never the SPA shell. Otherwise CLI
// tools see HTML on unknown API/RPC paths and misreport.
func TestBackendPrefixesNeverFallToSPA(t *testing.T) {
	h := New(Options{Version: "x", Logger: slog.Default()})
	cases := []string{
		"/api/nope",
		"/api/v1/does/not/exist",
		"/rpc/some.unknown.Service/Method",
		"/user-api/unknown",
	}
	for _, p := range cases {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q, want JSON (not SPA HTML)", p, ct)
		}
		if rec.Code == http.StatusOK {
			t.Errorf("%s: status = 200, want 4xx", p)
		}
	}
}

// TestMissingAssetReturns404 — a request under /assets/ for a file that does
// not exist must NOT be redirected to index.html (that breaks browser MIME
// checks for hashed JS bundles); it must 404 cleanly.
func TestMissingAssetReturns404(t *testing.T) {
	h := New(Options{Version: "x", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/assets/this-asset-does-not-exist.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want non-HTML", ct)
	}
}
