// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	kafkitov1 "github.com/FinkeFlo/kafkito/pkg/proto/kafkito/v1"
	"github.com/FinkeFlo/kafkito/pkg/proto/kafkito/v1/kafkitov1connect"
)

// TestConnectInfoService verifies the Connect-RPC InfoService is reachable
// in parallel to the existing REST /api/v1/info endpoint.
func TestConnectInfoService(t *testing.T) {
	h := New(Options{Version: "test-1.2.3"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := kafkitov1connect.NewInfoServiceClient(
		http.DefaultClient,
		srv.URL+"/rpc",
	)

	resp, err := client.GetInfo(t.Context(), connect.NewRequest(&kafkitov1.GetInfoRequest{}))
	require.NoError(t, err)
	require.Equal(t, "kafkito", resp.Msg.GetName())
	require.Equal(t, "test-1.2.3", resp.Msg.GetVersion())

	// REST surface still works in parallel.
	rest, err := http.Get(srv.URL + "/api/v1/info")
	require.NoError(t, err)
	defer func() { _ = rest.Body.Close() }()
	require.Equal(t, http.StatusOK, rest.StatusCode)
	require.True(t, strings.HasPrefix(rest.Header.Get("Content-Type"), "application/json"))
}
