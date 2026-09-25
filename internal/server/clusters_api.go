// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// ListClusters returns the configured clusters with a live reachability probe.
func (s *apiServer) ListClusters(ctx context.Context, _ gen.ListClustersRequestObject) (gen.ListClustersResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return gen.ListClusters200JSONResponse{Clusters: s.reg.Describe(ctx, 1*time.Second)}, nil
}

// TestCluster probes a user-supplied ClusterConfig (sent either as the
// request body or as the X-Kafkito-Cluster header, with body winning) and
// reports reachability plus a short capability probe. Used by the frontend
// settings UI to validate private-cluster credentials before storing them
// in the browser.
func (s *apiServer) TestCluster(ctx context.Context, req gen.TestClusterRequestObject) (gen.TestClusterResponseObject, error) {
	var cfg config.ClusterConfig
	if req.Body != nil {
		cfg = *req.Body
		if err := validateClusterPolicy(cfg); err != nil {
			return nil, badRequest(err.Error())
		}
	} else if ctxCfg, ok := privateClusterFromContext(ctx); ok {
		cfg = ctxCfg
	} else {
		return nil, badRequest("cluster config required in body or " + PrivateClusterHeader + " header")
	}
	name, err := s.reg.UseAdhoc(cfg)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	// Cheap config validation: build the kgo client up-front so that
	// misconfigured TLS / unparseable broker URLs surface as a 400 here
	// rather than burning the full Ping budget. Client construction is
	// synchronous and does not dial; the resulting client is cached on
	// the registry, so the subsequent Ping reuses it.
	if _, cerr := s.reg.Client(name); cerr != nil {
		return nil, badRequest(cerr.Error())
	}
	pingCtx, pingCancel := context.WithTimeout(ctx, s.testConnectionTimeout())
	defer pingCancel()
	info := kafkapkg.ClusterInfo{
		Name:           "",
		IsProd:         cfg.IsProd,
		AuthType:       strings.ToLower(strings.TrimSpace(cfg.Auth.Type)),
		TLS:            cfg.TLS.Enabled,
		SchemaRegistry: strings.TrimSpace(cfg.SchemaRegistry.URL) != "",
	}
	if info.AuthType == "" {
		info.AuthType = "none"
	}
	if err := s.reg.Ping(pingCtx, name); err != nil {
		// Intentional: testCluster is a user-invoked diagnostic for a cluster
		// the caller supplied and owns. Returning the raw connection error is
		// the point of this endpoint — it tells the user exactly why their
		// broker is unreachable (wrong host/port, TLS mismatch, SASL failure,
		// etc.). This is NOT an accidental upstream-error leak; do not route
		// through upstreamError here.
		if s.log != nil {
			s.log.ErrorContext(pingCtx, "testCluster ping failed", "err", err)
		}
		info.Reachable = false
		info.Error = err.Error()
	} else {
		info.Reachable = true
		capCtx, capCancel := context.WithTimeout(ctx, 4*time.Second)
		if caps, cerr := s.reg.Capabilities(capCtx, name); cerr == nil {
			info.Capabilities = caps
		}
		capCancel()
	}
	return gen.TestCluster200JSONResponse(info), nil
}

// GetCapabilities returns the cached capability probe for a cluster.
func (s *apiServer) GetCapabilities(ctx context.Context, req gen.GetCapabilitiesRequestObject) (gen.GetCapabilitiesResponseObject, error) {
	caps, err := s.capabilities(ctx, req.Cluster)
	if err != nil {
		return nil, err
	}
	return gen.GetCapabilities200JSONResponse{Cluster: req.Cluster, Capabilities: caps}, nil
}

// RefreshCapabilities invalidates the probe cache and re-runs it.
func (s *apiServer) RefreshCapabilities(ctx context.Context, req gen.RefreshCapabilitiesRequestObject) (gen.RefreshCapabilitiesResponseObject, error) {
	s.reg.RefreshCapabilities(req.Cluster)
	caps, err := s.capabilities(ctx, req.Cluster)
	if err != nil {
		return nil, err
	}
	return gen.RefreshCapabilities200JSONResponse{Cluster: req.Cluster, Capabilities: caps}, nil
}

func (s *apiServer) capabilities(ctx context.Context, cluster string) (kafkapkg.Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	caps, err := s.reg.Capabilities(ctx, cluster)
	if err != nil {
		return kafkapkg.Capabilities{}, clusterError(cluster, "get capabilities", err)
	}
	if caps == nil {
		return kafkapkg.Capabilities{}, nil
	}
	return *caps, nil
}

// ListBrokers returns the brokers of a cluster.
func (s *apiServer) ListBrokers(ctx context.Context, req gen.ListBrokersRequestObject) (gen.ListBrokersResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	brokers, err := s.reg.ListBrokers(ctx, req.Cluster)
	if err != nil {
		return nil, clusterError(req.Cluster, "list brokers", err)
	}
	return gen.ListBrokers200JSONResponse{Cluster: req.Cluster, Brokers: brokers}, nil
}
