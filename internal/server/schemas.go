// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// maxRegisterSchemaBodyBytes caps the register-schema body; schemas with
// their references can be large.
const maxRegisterSchemaBodyBytes = 2 << 20

// schemaRegistry returns the Schema Registry client of cluster. For a
// private cluster it is built from the X-Kafkito-Cluster config.
func (s *apiServer) schemaRegistry(cluster string) (*kafkapkg.SchemaRegistryClient, error) {
	sr, err := s.reg.SchemaRegistry(cluster)
	switch {
	case err == nil:
		return sr, nil
	case errors.Is(err, kafkapkg.ErrNoSchemaRegistry):
		return nil, &apiError{Status: http.StatusNotFound, Message: "schema registry not configured for cluster: " + cluster, Err: err}
	}
	return nil, clusterError(cluster, "schema registry client", err)
}

// ListSubjects lists the Schema Registry subjects with their versions.
func (s *apiServer) ListSubjects(ctx context.Context, req gen.ListSubjectsRequestObject) (gen.ListSubjectsResponseObject, error) {
	sr, err := s.schemaRegistry(req.Cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	subs, err := sr.ListSubjectsWithVersions(ctx)
	if err != nil {
		return nil, upstreamError("list subjects", err)
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name < subs[j].Name })
	return gen.ListSubjects200JSONResponse{Cluster: req.Cluster, Subjects: subs}, nil
}

// ListSchemaVersions lists the versions of a subject.
func (s *apiServer) ListSchemaVersions(ctx context.Context, req gen.ListSchemaVersionsRequestObject) (gen.ListSchemaVersionsResponseObject, error) {
	sr, err := s.schemaRegistry(req.Cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	vs, err := sr.ListVersions(ctx, req.Subject)
	if err != nil {
		return nil, upstreamError("list versions", err)
	}
	return gen.ListSchemaVersions200JSONResponse{Subject: req.Subject, Versions: vs}, nil
}

// GetSchemaVersion returns one version of a subject; the Schema Registry
// resolves the version ("latest" or a number).
func (s *apiServer) GetSchemaVersion(ctx context.Context, req gen.GetSchemaVersionRequestObject) (gen.GetSchemaVersionResponseObject, error) {
	sr, err := s.schemaRegistry(req.Cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, err := sr.GetVersion(ctx, req.Subject, req.Version)
	if err != nil {
		return nil, upstreamError("get schema version", err)
	}
	return gen.GetSchemaVersion200JSONResponse(*v), nil
}

// RegisterSchema registers a new schema version under a subject.
func (s *apiServer) RegisterSchema(ctx context.Context, req gen.RegisterSchemaRequestObject) (gen.RegisterSchemaResponseObject, error) {
	sr, err := s.schemaRegistry(req.Cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := sr.RegisterSchema(ctx, req.Subject, *req.Body)
	if err != nil {
		return nil, upstreamError("register schema", err)
	}
	return gen.RegisterSchema200JSONResponse(*res), nil
}

// DeleteSubject deletes a subject, soft by default.
// strictPermanentFlag accepts only the literal `true` and `false` for the
// deleteSubject permanent flag. The binding would also take 1, t or TRUE
// (strconv.ParseBool), which used to mean a soft delete; a hard delete
// cannot be undone, so those spellings are rejected instead.
func strictPermanentFlag(r *http.Request) error {
	if r == nil {
		return nil
	}
	for _, v := range r.URL.Query()["permanent"] {
		if v != "true" && v != "false" {
			return &apiError{Status: http.StatusBadRequest, Code: invalidRequestCode, Message: `parameter "permanent" in query: must be true or false`}
		}
	}
	return nil
}

func (s *apiServer) DeleteSubject(ctx context.Context, req gen.DeleteSubjectRequestObject) (gen.DeleteSubjectResponseObject, error) {
	sr, err := s.schemaRegistry(req.Cluster)
	if err != nil {
		return nil, err
	}
	if err := strictPermanentFlag(httpRequestFromContext(ctx)); err != nil {
		return nil, err
	}
	permanent := deref(req.Params.Permanent)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	versions, err := sr.DeleteSubject(ctx, req.Subject, permanent)
	if err != nil {
		return nil, upstreamError("delete subject", err)
	}
	return gen.DeleteSubject200JSONResponse{Deleted: req.Subject, Versions: versions, Permanent: permanent}, nil
}
