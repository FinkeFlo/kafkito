// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/rbac"
)

// Codes of the 403 for a caller that private_clusters.mode does not allow
// private clusters.
const (
	privateClustersDisabledCode  = "private_clusters_disabled"
	privateClustersForbiddenCode = "private_clusters_forbidden"
)

// testClusterRoute is Test connection, which probes a private cluster
// definition from the body or the X-Kafkito-Cluster header.
const testClusterRoute = "POST /api/v1/clusters/_test"

// privateClusterAccess applies private_clusters.mode. Every request that
// uses a private cluster goes through check: those for __private__ and Test
// connection in privateClusterGate, a copy into a dest_cluster_config in
// copyJobFor.
type privateClusterAccess struct {
	cfg    config.PrivateClustersConfig
	policy *rbac.Policy
}

// mode returns the effective mode. A value config.Validate refuses counts
// as off.
func (a privateClusterAccess) mode() config.PrivateClusterMode {
	switch m := a.cfg.EffectiveMode(); m {
	case config.PrivateClustersOn, config.PrivateClustersRole:
		return m
	default:
		return config.PrivateClustersOff
	}
}

// check returns nil when the caller of r may use private clusters, else the
// 403 to answer with. In mode role that takes private_cluster:use for the
// RBAC subject, and RBAC must be enabled.
func (a privateClusterAccess) check(r *http.Request) error {
	switch a.mode() {
	case config.PrivateClustersOn:
		return nil
	case config.PrivateClustersRole:
		if a.policy != nil && a.policy.Enabled() &&
			a.policy.Allow(rbacSubject(r, a.policy), "", rbac.ResourcePrivateCluster, "", rbac.ActionUse) {
			return nil
		}
		return &apiError{
			Status:  http.StatusForbidden,
			Code:    privateClustersForbiddenCode,
			Message: "your role does not allow private clusters",
		}
	default:
		return &apiError{
			Status:  http.StatusForbidden,
			Code:    privateClustersDisabledCode,
			Message: "private clusters are disabled",
		}
	}
}

// usesPrivateCluster reports whether r uses a private cluster by its route:
// a {cluster} of __private__, or Test connection. A copy into a
// dest_cluster_config is checked by its handler.
func usesPrivateCluster(r *http.Request) bool {
	if cluster, err := pathParam(r, "cluster"); err == nil && cluster == config.PrivateClusterSentinel {
		return true
	}
	return r.Method+" "+routePattern(r) == testClusterRoute
}

// privateClusterGate applies access before privateClusterMiddleware reads
// the X-Kafkito-Cluster header. When the caller may not use private
// clusters, a request that uses one gets the 403 of check, and any other
// request continues without the header, so the header is never decoded or
// validated.
func privateClusterGate(access privateClusterAccess, errs errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			private := usesPrivateCluster(r)
			header := len(r.Header.Values(PrivateClusterHeader)) > 0
			if !private && !header {
				next.ServeHTTP(w, r)
				return
			}
			err := access.check(r)
			switch {
			case err == nil:
			case private:
				errs.writeError(w, r, err)
				return
			default:
				r = r.Clone(r.Context())
				r.Header.Del(PrivateClusterHeader)
			}
			next.ServeHTTP(w, r)
		})
	}
}
