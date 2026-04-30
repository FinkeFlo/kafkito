// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import "net/http"

// RequireScope returns middleware that 403s requests whose principal lacks scope.
// 401s if no principal is in context (i.e. Middleware did not run upstream).
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFromContext(r.Context())
			if !ok {
				deny(w, "missing principal")
				return
			}
			if !p.HasScope(scope) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden","message":"scope ` + scope + ` required"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
