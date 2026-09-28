// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// maxSCRAMBodyBytes caps the upsert SCRAM user body.
const maxSCRAMBodyBytes = 16 << 10

// scramMechanisms are the mechanisms deleteScramUser tries when the request
// names none.
var scramMechanisms = []gen.DeleteScramUserParamsMechanism{gen.DeleteScramUserParamsMechanismSCRAMSHA256, gen.DeleteScramUserParamsMechanismSCRAMSHA512}

// ListScramUsers returns the users with SCRAM credentials, filtered by the
// caller's RBAC user:view permission. Private clusters are not filtered:
// RBAC does not apply to them.
func (s *apiServer) ListScramUsers(ctx context.Context, req gen.ListScramUsersRequestObject) (gen.ListScramUsersResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	users, err := s.scram.ListSCRAMUsers(ctx, req.Cluster)
	if err != nil {
		return nil, clusterError(req.Cluster, "list SCRAM users", err)
	}
	if s.policy != nil && s.policy.Enabled() && !kafkapkg.IsAdhoc(req.Cluster) {
		user := rbacSubject(httpRequestFromContext(ctx), s.policy)
		users = filterSCRAMUsersByRBAC(users, s.policy, user, req.Cluster)
	}
	return gen.ListScramUsers200JSONResponse{Cluster: req.Cluster, Users: users}, nil
}

// UpsertScramUser creates or updates a SCRAM credential. The password is
// passed to the broker only; it is not part of any response or log line.
func (s *apiServer) UpsertScramUser(ctx context.Context, req gen.UpsertScramUserRequestObject) (gen.UpsertScramUserResponseObject, error) {
	b := req.Body
	// rbacMiddleware authorized the trimmed user name; the broker stores
	// the name as sent, so a padded name must be allowed as sent too.
	if b.User != strings.TrimSpace(b.User) && s.policy != nil && s.policy.Enabled() && !kafkapkg.IsAdhoc(req.Cluster) {
		user := rbacSubject(httpRequestFromContext(ctx), s.policy)
		if !s.policy.Allow(user, req.Cluster, "user", b.User, "edit") {
			return nil, &apiError{Status: http.StatusForbidden, Code: "rbac_denied", Message: "forbidden"}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.scram.UpsertSCRAMUser(ctx, req.Cluster, b.User, string(b.Mechanism), b.Password, deref(b.Iterations)); err != nil {
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
		if err := s.scram.DeleteSCRAMUser(ctx, req.Cluster, req.User, string(m)); err != nil {
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

// filterSCRAMUsersByRBAC removes the SCRAM users the user is not allowed to
// view.
func filterSCRAMUsersByRBAC(users []kafkapkg.SCRAMUser, policy *rbac.Policy, user, cluster string) []kafkapkg.SCRAMUser {
	globs, all := policy.AllowedResourceNames(user, cluster, "user", "view")
	if all {
		return users
	}
	if len(globs) == 0 {
		return []kafkapkg.SCRAMUser{}
	}
	out := make([]kafkapkg.SCRAMUser, 0, len(users))
	for _, u := range users {
		for _, glob := range globs {
			if rbac.MatchName(glob, u.User) {
				out = append(out, u)
				break
			}
		}
	}
	return out
}
