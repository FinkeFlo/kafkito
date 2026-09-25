// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"
	"strings"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// contentSecurityPolicy returns the CSP sent on every response. The SPA is
// built so that everything it loads comes from its own origin: no inline
// scripts (the theme bootstrap is /theme-init.js), no runtime-injected
// <style> elements (see the sonner plugin in frontend/vite.config.ts), and
// API calls go to the same origin. frameAncestors is the configured
// server.frame_ancestors source list.
func contentSecurityPolicy(frameAncestors string) string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"font-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors " + frameAncestors,
	}, "; ")
}

// securityHeadersMiddleware sets browser hardening headers on every response
// (API, SPA shell and static assets). Private-cluster credentials live in
// the browser's localStorage, so a strict CSP is the main defence against an
// XSS turning into a credential leak.
//
// HSTS is deliberately not set: TLS is terminated by the upstream proxy,
// which owns that header.
func securityHeadersMiddleware(frameAncestors string) func(http.Handler) http.Handler {
	frameAncestors = strings.TrimSpace(frameAncestors)
	if frameAncestors == "" {
		frameAncestors = config.DefaultFrameAncestors
	}
	csp := contentSecurityPolicy(frameAncestors)
	// X-Frame-Options only for legacy browsers that ignore frame-ancestors;
	// it cannot express an allow-list, so it is omitted when framing is
	// allowed.
	denyFraming := frameAncestors == config.DefaultFrameAncestors
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if denyFraming {
				h.Set("X-Frame-Options", "DENY")
			}
			next.ServeHTTP(w, r)
		})
	}
}
