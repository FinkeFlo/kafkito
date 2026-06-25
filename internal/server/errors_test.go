// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayError_HidesDetailFromClientButLogsIt(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	rec := httptest.NewRecorder()
	secret := errors.New("dial broker-internal-7.corp.local:9092: connection refused")

	gatewayError(context.Background(), rec, log, "list consumers", secret)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "kafka_upstream", body["code"])
	assert.NotContains(t, rec.Body.String(), "broker-internal-7.corp.local",
		"internal hostname must not leak to the client")
	assert.True(t, strings.Contains(buf.String(), "broker-internal-7.corp.local"),
		"full error must be logged server-side")
}
