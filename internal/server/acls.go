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

// maxACLBodyBytes caps the create and delete ACL bodies.
const maxACLBodyBytes = 16 << 10

// ListAcls enumerates the ACLs visible on the cluster.
func (s *apiServer) ListAcls(ctx context.Context, req gen.ListAclsRequestObject) (gen.ListAclsResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	acls, err := s.reg.ListACLs(ctx, req.Cluster)
	if err != nil {
		return nil, clusterError(req.Cluster, "list ACLs", err)
	}
	return gen.ListAcls200JSONResponse{Cluster: req.Cluster, Acls: acls}, nil
}

// CreateAcl creates a single ACL binding on the cluster.
func (s *apiServer) CreateAcl(ctx context.Context, req gen.CreateAclRequestObject) (gen.CreateAclResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.reg.CreateACL(ctx, req.Cluster, kafkapkg.ACLSpec(*req.Body)); err != nil {
		return nil, aclError(req.Cluster, "create ACL", err)
	}
	return gen.CreateAcl201JSONResponse{Ok: true, Acl: *req.Body}, nil
}

// DeleteAcl removes the ACL bindings matching the filter in the body.
func (s *apiServer) DeleteAcl(ctx context.Context, req gen.DeleteAclRequestObject) (gen.DeleteAclResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deleted, err := s.reg.DeleteACL(ctx, req.Cluster, kafkapkg.ACLSpec(*req.Body))
	if err != nil {
		return nil, aclError(req.Cluster, "delete ACL", err)
	}
	return gen.DeleteAcl200JSONResponse{Ok: true, Deleted: deleted}, nil
}

// aclError maps a failed ACL write: input the broker or franz-go rejected
// is a 400 naming the reason, anything else an upstream error.
func aclError(cluster, op string, err error) error {
	if msg := err.Error(); !errors.Is(err, kafkapkg.ErrUnknownCluster) && isACLClientErr(msg) {
		return badRequest("kafka: " + msg)
	}
	return clusterError(cluster, op, err)
}

// isACLClientErr reports whether a Kafka ACL error originates from bad
// caller-supplied input (400) rather than a broker-side failure (502).
func isACLClientErr(msg string) bool {
	return strings.Contains(msg, "required") || strings.Contains(msg, "validate") ||
		strings.Contains(msg, "resource_type") || strings.Contains(msg, "pattern_type") ||
		strings.Contains(msg, "operation") || strings.Contains(msg, "permission_type")
}
