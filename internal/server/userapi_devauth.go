// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

//go:build devauth

package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// mountUserAPIStub mounts /user-api/currentUser with a fake user document so
// the SPA's AuthProvider has something to consume during local dev. The
// approuter normally serves this endpoint in production; in dev we run
// approuter-less, so the Go binary stands in.
func mountUserAPIStub(r chi.Router) {
	r.Get("/user-api/currentUser", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"firstname":   "Dev",
			"lastname":    "User",
			"email":       "dev@local",
			"name":        "dev@local",
			"displayName": "Dev User (dev@local)",
		})
	})
}
