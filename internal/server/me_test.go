// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/server"
	"github.com/FinkeFlo/kafkito/pkg/config"
)

type stubValidator struct{ p *auth.Principal }

func (s stubValidator) Validate(context.Context, string) (*auth.Principal, error) {
	return s.p, nil
}

func TestHandleMe_WithJWT(t *testing.T) {
	v := stubValidator{p: &auth.Principal{
		Subject:  "u-1",
		UserName: "testuser",
		Email:    "f@x",
		Scopes:   []string{"Display"},
		Tenant:   "test-zone",
	}}
	h := server.New(server.Options{Version: "test", Config: config.Config{}, Auth: v})

	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer ignored-by-stub")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["user"] != "testuser" {
		t.Errorf("user = %v, want testuser", body["user"])
	}
	if body["jwt"] != true {
		t.Errorf("jwt flag = %v, want true", body["jwt"])
	}
	if body["tenant"] != "test-zone" {
		t.Errorf("tenant = %v", body["tenant"])
	}
	scopes, _ := body["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "Display" {
		t.Errorf("scopes = %v", body["scopes"])
	}
}

func TestHandleMe_WithoutJWT(t *testing.T) {
	// No Auth wired -> Principal is absent -> falls through to header trust path
	h := server.New(server.Options{Version: "test", Config: config.Config{}})

	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["jwt"] != false {
		t.Errorf("jwt flag = %v, want false", body["jwt"])
	}
}
