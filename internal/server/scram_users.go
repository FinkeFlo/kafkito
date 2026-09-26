// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"strings"
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// maxSCRAMBodyBytes caps the upsert SCRAM user body.
const maxSCRAMBodyBytes = 16 << 10

// scramMechanisms are the mechanisms deleteScramUser tries when the request
// names none.
var scramMechanisms = []gen.DeleteScramUserParamsMechanism{gen.DeleteScramUserParamsMechanismSCRAMSHA256, gen.DeleteScramUserParamsMechanismSCRAMSHA512}

// ListScramUsers returns all users with SCRAM credentials.
func (s *apiServer) ListScramUsers(ctx context.Context, req gen.ListScramUsersRequestObject) (gen.ListScramUsersResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	users, err := s.reg.ListSCRAMUsers(ctx, req.Cluster)
	if err != nil {
		return nil, clusterError(req.Cluster, "list SCRAM users", err)
	}
	return gen.ListScramUsers200JSONResponse{Cluster: req.Cluster, Users: users}, nil
}

// UpsertScramUser creates or updates a SCRAM credential. The password is
// passed to the broker only; it is not part of any response or log line.
func (s *apiServer) UpsertScramUser(ctx context.Context, req gen.UpsertScramUserRequestObject) (gen.UpsertScramUserResponseObject, error) {
	b := req.Body
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.reg.UpsertSCRAMUser(ctx, req.Cluster, b.User, string(b.Mechanism), b.Password, deref(b.Iterations)); err != nil {
		return nil, scramError(req.Cluster, "upsert SCRAM user", err)
	}
	return gen.UpsertScramUser200JSONResponse{Ok: true, User: b.User, Mechanism: string(b.Mechanism)}, nil
}

// DeleteScramUser deletes the user's credential for the mechanism in the
// query, or for both mechanisms if it names none. It succeeds if at least
// one credential was deleted.
func (s *apiServer) DeleteScramUser(ctx context.Context, req gen.DeleteScramUserRequestObject) (gen.DeleteScramUserResponseObject, error) {
	mechs := scramMechanisms
	if m := req.Params.Mechanism; m != nil {
		mechs = []gen.DeleteScramUserParamsMechanism{*m}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deleted := 0
	var lastErr error
	for _, m := range mechs {
		if err := s.reg.DeleteSCRAMUser(ctx, req.Cluster, req.User, string(m)); err != nil {
			lastErr = err
			continue
		}
		deleted++
	}
	if deleted == 0 && lastErr != nil {
		return nil, scramError(req.Cluster, "delete SCRAM user", lastErr)
	}
	return gen.DeleteScramUser200JSONResponse{Ok: true, User: req.User, Deleted: deleted}, nil
}

// scramError maps a failed SCRAM write: input the registry rejected is a
// 400 naming the reason, anything else an upstream error.
func scramError(cluster, op string, err error) error {
	if msg := err.Error(); !errors.Is(err, kafkapkg.ErrUnknownCluster) && isSCRAMClientErr(msg) {
		return badRequest("kafka: " + msg)
	}
	return clusterError(cluster, op, err)
}

// isSCRAMClientErr reports whether a Kafka SCRAM error originates from bad
// caller-supplied input (400) rather than a broker-side failure (502).
func isSCRAMClientErr(msg string) bool {
	return strings.Contains(msg, "required") || strings.Contains(msg, "mechanism") ||
		strings.Contains(msg, "iterations")
}
