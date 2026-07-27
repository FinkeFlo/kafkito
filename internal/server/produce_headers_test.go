// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

func TestInjectKafkitoProduceHeaders(t *testing.T) {
	t.Parallel()

	t.Run("initializes headers and sets source", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{}
		injectKafkitoProduceHeaders(&req, "")

		require.NotNil(t, req.Headers)
		assert.Equal(t, "true", req.Headers["X-Kafkito-Source"])
		_, hasUser := req.Headers["X-Kafkito-User"]
		assert.False(t, hasUser)
	})

	t.Run("preserves caller headers and adds metadata", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{
			Headers: map[string]string{
				"X-Custom": "value",
			},
		}
		injectKafkitoProduceHeaders(&req, "alice")

		assert.Equal(t, "value", req.Headers["X-Custom"])
		assert.Equal(t, "true", req.Headers["X-Kafkito-Source"])
		assert.Equal(t, "alice", req.Headers["X-Kafkito-User"])
	})

	t.Run("overwrites spoofed metadata headers", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{
			Headers: map[string]string{
				"X-Kafkito-Source": "false",
				"X-Kafkito-User":   "spoofed-user",
			},
		}
		injectKafkitoProduceHeaders(&req, "real-user")

		assert.Equal(t, "true", req.Headers["X-Kafkito-Source"])
		assert.Equal(t, "real-user", req.Headers["X-Kafkito-User"])
	})
}
