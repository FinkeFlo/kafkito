// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import "net/http"

// MiddlewareFor returns the correct http.Handler middleware for the given
// Validator. If v implements syntheticValidator (i.e. the devauth off-mode
// alwaysAllowValidator) the middleware injects the synthetic Principal on
// every request without requiring a Bearer header. Otherwise it delegates to
// Middleware which enforces bearer token validation.
func MiddlewareFor(v Validator) func(http.Handler) http.Handler {
	if sv, ok := v.(syntheticValidator); ok {
		return MiddlewareWithSyntheticPrincipal(sv.SyntheticPrincipal())
	}
	return Middleware(v)
}

// syntheticValidator is an optional interface implemented by validators that
// inject a fixed Principal without inspecting the request (e.g. off mode).
type syntheticValidator interface {
	SyntheticPrincipal() *Principal
}

// MiddlewareWithSyntheticPrincipal injects p on every request without checking any header.
// Used by `off` mode so local dev does not need a token at all.
func MiddlewareWithSyntheticPrincipal(p *Principal) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}
