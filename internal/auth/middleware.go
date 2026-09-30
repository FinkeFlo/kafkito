// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// Middleware returns an http middleware that validates the bearer token via v
// and stores the resulting Principal in the request context. On any failure it
// responds with 401 and writes a structured error.
func Middleware(v Validator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				deny(w, "missing bearer token")
				return
			}
			p, err := v.Validate(r.Context(), raw)
			if err != nil {
				slog.DebugContext(r.Context(), "auth validate failed", "err", err)
				deny(w, "invalid token")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// bearerToken returns the token of a Bearer Authorization header, or "".
// The scheme is matched case-insensitively (RFC 7235 section 2.1), and
// whitespace around the value and after the scheme is dropped.
func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	i := strings.IndexAny(h, " \t")
	if i < 0 || !strings.EqualFold(h[:i], "Bearer") {
		return ""
	}
	return strings.TrimSpace(h[i+1:])
}

func deny(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="kafkito"`)
	w.WriteHeader(http.StatusUnauthorized)
	body, _ := json.Marshal(map[string]string{"error": "unauthorized", "message": msg})
	_, _ = w.Write(body)
}
