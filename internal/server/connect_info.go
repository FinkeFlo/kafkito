// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"

	"connectrpc.com/connect"

	kafkitov1 "github.com/FinkeFlo/kafkito/pkg/proto/kafkito/v1"
	"github.com/FinkeFlo/kafkito/pkg/proto/kafkito/v1/kafkitov1connect"
)

// infoConnectHandler implements the InfoService Connect-RPC handler.
//
// It is mounted in parallel to the existing REST `/api/v1/info` endpoint;
// REST is the stable surface, Connect is the forward-looking one.
type infoConnectHandler struct {
	version string
}

func newInfoConnectHandler(version string) *infoConnectHandler {
	return &infoConnectHandler{version: version}
}

// GetInfo returns name + version, mirroring the REST handler.
func (h *infoConnectHandler) GetInfo(
	_ context.Context,
	_ *connect.Request[kafkitov1.GetInfoRequest],
) (*connect.Response[kafkitov1.GetInfoResponse], error) {
	return connect.NewResponse(&kafkitov1.GetInfoResponse{
		Name:    "kafkito",
		Version: h.version,
	}), nil
}

// Compile-time check.
var _ kafkitov1connect.InfoServiceHandler = (*infoConnectHandler)(nil)
