// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

//go:build !devauth

package server

import "github.com/go-chi/chi/v5"

// mountUserAPIStub is a no-op in default builds — the approuter owns the
// /user-api/* surface in production.
func mountUserAPIStub(_ chi.Router) {}
