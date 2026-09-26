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

// maxGroupBodyBytes caps the create-group and reset-offsets bodies. It equals
// maxJSONBodyBytes, the cap of the RBAC middleware's read of the create-group
// body (group_id).
const maxGroupBodyBytes = maxJSONBodyBytes

// ListGroups returns the consumer groups of a cluster, filtered by the
// caller's RBAC group:view permission.
func (s *apiServer) ListGroups(ctx context.Context, req gen.ListGroupsRequestObject) (gen.ListGroupsResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	groups, err := s.reg.ListGroups(ctx, req.Cluster)
	if err != nil {
		return nil, clusterError(req.Cluster, "list groups", err)
	}
	if s.policy != nil && s.policy.Enabled() {
		user := rbacSubject(httpRequestFromContext(ctx), s.policy)
		groups = filterGroupsByRBAC(groups, s.policy, user, req.Cluster)
	}
	return gen.ListGroups200JSONResponse{Cluster: req.Cluster, Groups: groups}, nil
}

// CreateGroup creates a new consumer group bound to a single topic.
func (s *apiServer) CreateGroup(ctx context.Context, req gen.CreateGroupRequestObject) (gen.CreateGroupResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := s.reg.CreateGroup(ctx, req.Cluster, *req.Body)
	if err != nil {
		switch {
		case errors.Is(err, kafkapkg.ErrUnknownCluster):
			return nil, clusterError(req.Cluster, "create group", err)
		case errors.Is(err, kafkapkg.ErrGroupExists):
			return nil, &apiError{Status: http.StatusConflict, Message: "kafka: " + err.Error(), Err: err}
		case errors.Is(err, kafkapkg.ErrNotAuthorized):
			// The broker's reason (e.g. GROUP_AUTHORIZATION_FAILED) is part
			// of the message the create-group dialog shows.
			return nil, &apiError{Status: http.StatusForbidden, Message: err.Error(), Err: err}
		}
		if msg := err.Error(); strings.Contains(msg, "required") || strings.Contains(msg, "unknown strategy") ||
			strings.Contains(msg, "not found") || strings.Contains(msg, "shift-by") {
			return nil, badRequest("kafka: " + msg)
		}
		return nil, upstreamError("create group", err)
	}
	return gen.CreateGroup200JSONResponse(*res), nil
}

// DescribeGroup returns detail for a consumer group.
func (s *apiServer) DescribeGroup(ctx context.Context, req gen.DescribeGroupRequestObject) (gen.DescribeGroupResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := s.reg.DescribeGroup(ctx, req.Cluster, req.Group)
	if err != nil {
		return nil, clusterError(req.Cluster, "describe group", err)
	}
	return gen.DescribeGroup200JSONResponse(*d), nil
}

// DeleteGroup removes a consumer group.
func (s *apiServer) DeleteGroup(ctx context.Context, req gen.DeleteGroupRequestObject) (gen.DeleteGroupResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := s.reg.DeleteGroup(ctx, req.Cluster, req.Group); err != nil {
		return nil, clusterError(req.Cluster, "delete group", err)
	}
	return gen.DeleteGroup200JSONResponse{Deleted: req.Group}, nil
}

// ResetGroupOffsets issues an offset reset for a single group and topic.
func (s *apiServer) ResetGroupOffsets(ctx context.Context, req gen.ResetGroupOffsetsRequestObject) (gen.ResetGroupOffsetsResponseObject, error) {
	if err := prodConfirmationError(s.reg, req.Cluster, httpRequestFromContext(ctx)); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := s.reg.ResetOffsets(ctx, req.Cluster, req.Group, *req.Body)
	if err != nil {
		if msg := err.Error(); !errors.Is(err, kafkapkg.ErrUnknownCluster) &&
			(strings.Contains(msg, "required") || strings.Contains(msg, "unknown strategy") || strings.Contains(msg, "not found")) {
			return nil, badRequest("kafka: " + msg)
		}
		return nil, clusterError(req.Cluster, "reset group offsets", err)
	}
	return gen.ResetGroupOffsets200JSONResponse(*res), nil
}

// filterGroupsByRBAC removes consumer groups the user is not allowed to view.
func filterGroupsByRBAC(groups []kafkapkg.GroupInfo, policy *rbac.Policy, user, cluster string) []kafkapkg.GroupInfo {
	globs, all := policy.AllowedResourceNames(user, cluster, "group", "view")
	if all {
		return groups
	}
	if len(globs) == 0 {
		return []kafkapkg.GroupInfo{}
	}
	out := make([]kafkapkg.GroupInfo, 0, len(groups))
	for _, g := range groups {
		for _, glob := range globs {
			if rbac.MatchName(glob, g.GroupID) {
				out = append(out, g)
				break
			}
		}
	}
	return out
}
