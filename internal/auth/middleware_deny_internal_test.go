// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeny_EmitsValidJSONEvenWithQuotesInMessage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	deny(rec, `weird "quoted" message`)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"body must be valid JSON even when msg contains quotes")
	assert.Equal(t, "unauthorized", body.Error)
	assert.Equal(t, `weird "quoted" message`, body.Message)
}
