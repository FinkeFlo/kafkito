// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkitov1 "github.com/FinkeFlo/kafkito/pkg/proto/kafkito/v1"
	"github.com/FinkeFlo/kafkito/pkg/proto/kafkito/v1/kafkitov1connect"
)

// TestInfoService_ExposesBothSurfaces verifies the InfoService is reachable
// over both Connect-RPC and the REST /api/v1/info endpoint, sharing a single
// in-process httptest.Server between the focused subtests.
func TestInfoService_ExposesBothSurfaces(t *testing.T) {
	t.Parallel()

	h := New(Options{Version: "test-1.2.3"})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	t.Run("exposes_connect_rpc_get_info", func(t *testing.T) {
		t.Parallel()

		client := kafkitov1connect.NewInfoServiceClient(
			http.DefaultClient,
			srv.URL+"/rpc",
		)

		resp, err := client.GetInfo(t.Context(), connect.NewRequest(&kafkitov1.GetInfoRequest{}))

		require.NoError(t, err)
		assert.Equal(t, "kafkito", resp.Msg.GetName())
		assert.Equal(t, "test-1.2.3", resp.Msg.GetVersion())
	})

	t.Run("exposes_rest_info_endpoint", func(t *testing.T) {
		t.Parallel()

		rest, err := http.Get(srv.URL + "/api/v1/info")
		require.NoError(t, err)
		t.Cleanup(func() { _ = rest.Body.Close() })

		assert.Equal(t, http.StatusOK, rest.StatusCode)
		assert.True(t,
			strings.HasPrefix(rest.Header.Get("Content-Type"), "application/json"),
			"Content-Type = %q", rest.Header.Get("Content-Type"))
	})
}
