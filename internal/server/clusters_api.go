// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/connerr"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// ListClusters returns the configured clusters with a live reachability probe.
func (s *apiServer) ListClusters(ctx context.Context, _ gen.ListClustersRequestObject) (gen.ListClustersResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return gen.ListClusters200JSONResponse{Clusters: s.clusters.Describe(ctx, 1*time.Second)}, nil
}

// TestCluster probes a user-supplied ClusterConfig (sent either as the
// request body or as the X-Kafkito-Cluster header, with body winning) and
// reports reachability plus a short capability probe. Used by the frontend
// settings UI to validate private-cluster credentials before storing them
// in the browser. privateClusterGate applies private_clusters.mode first.
func (s *apiServer) TestCluster(ctx context.Context, req gen.TestClusterRequestObject) (gen.TestClusterResponseObject, error) {
	var cfg config.ClusterConfig
	if req.Body != nil {
		cfg = *req.Body
		if err := validateClusterPolicy(ctx, cfg, s.privateClusters.cfg); err != nil {
			return nil, definitionError("", err)
		}
	} else if ctxCfg, ok := privateClusterFromContext(ctx); ok {
		cfg = ctxCfg
	} else {
		return nil, badRequest("cluster config required in body or " + PrivateClusterHeader + " header")
	}
	name, err := s.clusters.UseAdhoc(cfg)
	if err != nil {
		return nil, badRequest(errTextAdhocRegister)
	}
	// Cheap config validation: build the kgo client up-front so that
	// misconfigured TLS / unparseable broker URLs surface as a 400 here
	// rather than burning the full Ping budget. Client construction is
	// synchronous and does not dial; the resulting client is cached on
	// the registry, so the subsequent Ping reuses it. Its error can quote
	// a broker address, so the client gets a fixed text.
	if _, cerr := s.clusters.Client(name); cerr != nil {
		return nil, badRequest("private cluster client could not be created; check the broker addresses")
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
	if err := s.clusters.Ping(pingCtx, name); err != nil {
		// The caller learns the class of the failure (connerr), not the
		// raw error: that names resolved addresses, ports and OS errors
		// as seen from the server's network position. The log keeps it.
		if s.log != nil {
			s.log.WarnContext(pingCtx, "testCluster ping failed", "cluster", config.ClusterLogName(name), "err", err)
		}
		info.Reachable = false
		info.Error = connerr.Message(err)
		info.ErrorClass = connerr.Classify(err)
	} else if issues, skipped, perr := s.clusters.ProbeBrokers(pingCtx, name); perr != nil || len(issues) > 0 {
		// The seed answered, but kafkito talks to the brokers the cluster
		// advertises, not to the seed (#126). Each one must be reachable.
		// Every issue carries the class of its failure; host and port are
		// the ones the caller's own cluster advertises.
		info.Reachable = false
		info.BrokerIssues = issues
		info.BrokersSkipped = skipped
		logErr := perr
		if perr != nil {
			info.Error = connerr.Message(perr)
			info.ErrorClass = connerr.Classify(perr)
		} else {
			info.Error = brokerIssuesSummary(issues, skipped)
			logErr = brokerIssuesCause(issues)
		}
		if s.log != nil {
			s.log.WarnContext(pingCtx, "testCluster broker probe failed", "cluster", config.ClusterLogName(name), "err", logErr)
		}
	} else {
		// Brokers beyond the probe cap were not checked. That alone is no
		// reason to call the cluster unreachable; brokers_skipped tells the
		// caller the check was partial.
		info.Reachable = true
		info.BrokersSkipped = skipped
		capCtx, capCancel := context.WithTimeout(ctx, 4*time.Second)
		if caps, cerr := s.clusters.Capabilities(capCtx, name); cerr == nil {
			info.Capabilities = caps
		}
		capCancel()
	}
	return gen.TestCluster200JSONResponse(info), nil
}

// brokerIssuesSummary is the ClusterInfo.Error text for a cluster whose seed
// answered but whose advertised brokers did not all do so. skipped is the
// number of advertised brokers the probe did not check. It names each
// broker by node ID with the class text of its failure; the addresses are
// in the issues themselves.
func brokerIssuesSummary(issues []kafkapkg.BrokerIssue, skipped int) string {
	parts := make([]string, 0, len(issues))
	for _, is := range issues {
		parts = append(parts, fmt.Sprintf("node %d: %s", is.NodeID, is.Error))
	}
	summary := "some advertised brokers cannot be reached: " + strings.Join(parts, "; ")
	switch {
	case skipped == 1:
		summary += "; 1 more broker was not checked"
	case skipped > 1:
		summary += fmt.Sprintf("; %d more brokers were not checked", skipped)
	}
	return summary
}

// brokerIssuesCause is the server log counterpart of brokerIssuesSummary:
// the advertised address, reason and full error of every failed broker.
func brokerIssuesCause(issues []kafkapkg.BrokerIssue) error {
	parts := make([]string, 0, len(issues))
	for _, is := range issues {
		parts = append(parts, fmt.Sprintf("broker %d advertises %s (%s): %v",
			is.NodeID, net.JoinHostPort(is.Host, strconv.Itoa(int(is.Port))), is.Reason, is.Cause))
	}
	return errors.New(strings.Join(parts, "; "))
}

// GetCapabilities returns the cached capability probe for a cluster.
func (s *apiServer) GetCapabilities(ctx context.Context, req gen.GetCapabilitiesRequestObject) (gen.GetCapabilitiesResponseObject, error) {
	caps, err := s.capabilities(ctx, req.Cluster)
	if err != nil {
		return nil, err
	}
	return gen.GetCapabilities200JSONResponse{Cluster: config.PublicClusterName(req.Cluster), Capabilities: caps}, nil
}

// RefreshCapabilities invalidates the probe cache and re-runs it.
func (s *apiServer) RefreshCapabilities(ctx context.Context, req gen.RefreshCapabilitiesRequestObject) (gen.RefreshCapabilitiesResponseObject, error) {
	s.clusters.RefreshCapabilities(req.Cluster)
	caps, err := s.capabilities(ctx, req.Cluster)
	if err != nil {
		return nil, err
	}
	return gen.RefreshCapabilities200JSONResponse{Cluster: config.PublicClusterName(req.Cluster), Capabilities: caps}, nil
}

func (s *apiServer) capabilities(ctx context.Context, cluster string) (kafkapkg.Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	caps, err := s.clusters.Capabilities(ctx, cluster)
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
	brokers, err := s.clusters.ListBrokers(ctx, req.Cluster)
	if err != nil {
		return nil, clusterError(req.Cluster, "list brokers", err)
	}
	return gen.ListBrokers200JSONResponse{Cluster: config.PublicClusterName(req.Cluster), Brokers: brokers}, nil
}
