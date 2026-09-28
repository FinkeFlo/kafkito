// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
	"github.com/go-chi/chi/v5"
)

type rbacContextKey string

const rbacSubjectKey rbacContextKey = "subject"

func withSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, rbacSubjectKey, subject)
}

// rbacSubject resolves the RBAC identity for the request. A verified JWT
// principal (set by auth.Middleware) is authoritative: when present, the
// client-supplied identity header is ignored to prevent header-spoofing
// privilege escalation. UserName is preferred over Subject to match handleMe.
// The header is consulted only when no principal exists (auth disabled).
func rbacSubject(r *http.Request, policy *rbac.Policy) string {
	if p, ok := auth.PrincipalFromContext(r.Context()); ok {
		if p.UserName != "" {
			return p.UserName
		}
		return p.Subject
	}
	return r.Header.Get(policy.Header())
}

// rbacExemptRoutes are the routes ("METHOD pattern") RBAC lets through
// without a resource permission, each with the reason. rbacMiddleware denies
// any other route resolvePermission has no permission for, so a new route
// is closed until it is mapped or listed here.
var rbacExemptRoutes = map[string]string{
	"POST /api/v1/clusters/_test": "probes a cluster definition sent in the body, " +
		"like a private cluster (which RBAC does not apply to); no configured " +
		"cluster is involved and the outbound-host guard applies",
}

// rbacMiddleware enforces RBAC for cluster routes. The identity is resolved
// from the configured header; the resource/action is derived from the matched
// chi route pattern and HTTP method, the resource names from the path
// parameters as the handler binds them (see pathParam). A route without a
// permission is denied unless it is in rbacExemptRoutes or addresses a
// private cluster.
func rbacMiddleware(policy *rbac.Policy, log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := rbacSubject(r, policy)
			r = r.WithContext(withSubject(r.Context(), user))

			if !policy.Enabled() {
				next.ServeHTTP(w, r)
				return
			}
			// Never decide on a name the binding cannot decode or on an
			// empty one (the "any name" case); the binding rejects both.
			if k := invalidPathParam(r); k != "" {
				writeInvalidPathParam(w, k)
				return
			}

			resType, resName, action, bodyField := resolvePermission(r)
			if resType == "" {
				pattern := routePattern(r)
				cluster, _ := pathParam(r, "cluster")
				if _, ok := rbacExemptRoutes[r.Method+" "+pattern]; ok || cluster == config.PrivateClusterSentinel {
					next.ServeHTTP(w, r)
					return
				}
				log.WarnContext(r.Context(), "rbac: route has no permission mapping, denied",
					"method", r.Method, "route", pattern)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden", "code": "rbac_denied"})
				return
			}

			if bodyField != "" {
				// Runs before the route's own body limit, so it caps the read
				// itself, at that limit and with that message.
				limit, prefix := rbacBodyLimit(resType)
				bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
				_ = r.Body.Close()
				if err != nil {
					msg := "failed to read request body"
					var mbe *http.MaxBytesError
					if errors.As(err, &mbe) {
						msg = prefix + mbe.Error()
					}
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
					return
				}
				var fields map[string]json.RawMessage
				var name string
				if err := json.Unmarshal(bodyBytes, &fields); err == nil {
					if raw, ok := fields[bodyField]; ok {
						_ = json.Unmarshal(raw, &name)
					}
				}
				name = strings.TrimSpace(name)
				if name == "" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid '" + bodyField + "' in request body"})
					return
				}
				resName = name
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			cluster, _ := pathParam(r, "cluster")
			// Private clusters bypass RBAC entirely: the user supplies their
			// own Kafka credentials via the X-Kafkito-Cluster header, and the
			// broker enforces its own ACLs.
			if cluster == config.PrivateClusterSentinel {
				next.ServeHTTP(w, r)
				return
			}
			if !policy.Allow(user, cluster, resType, resName, action) {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error":    "forbidden",
					"resource": resType + ":" + resName,
					"action":   action,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rbacListSubject reports whether a handler applies RBAC itself on cluster
// (to filter a list or check a body name), and for which identity. It does
// not when RBAC is disabled or for a private (ad-hoc) cluster, which RBAC
// does not apply to.
func (s *apiServer) rbacListSubject(ctx context.Context, cluster string) (string, bool) {
	if s.policy == nil || !s.policy.Enabled() || kafkapkg.IsAdhoc(cluster) {
		return "", false
	}
	return rbacSubject(httpRequestFromContext(ctx), s.policy), true
}

// routePattern is the matched chi route pattern of r, or "".
func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		return rctx.RoutePattern()
	}
	return ""
}

// rbacBodyLimit returns the body limit and too-large message prefix of the
// route whose body rbacMiddleware reads to find the resource name: those of
// create topic and create group, or of the SCRAM user upsert.
func rbacBodyLimit(resType string) (int64, string) {
	if resType == "user" {
		return maxSCRAMBodyBytes, "invalid json: "
	}
	return maxJSONBodyBytes, "invalid body: "
}

// resolvePermission maps the current request to (resourceType, resourceName,
// action, bodyField). A non-empty bodyField names the JSON request-body field
// that holds the resource name; the middleware reads it to derive resName
// (e.g. POST /topics uses "name", POST /groups uses "group_id", POST /users
// uses "user"). An empty bodyField means resName is already final. A return of ("", "", "", "")
// means no permission check is required.
func resolvePermission(r *http.Request) (resType, resName, action, bodyField string) {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return
	}
	method := r.Method
	pattern := rctx.RoutePattern()

	// rbacMiddleware rejects undecodable and empty parameters before it
	// gets here; the fallback is "", never the raw value.
	param := func(key string) string {
		v, err := pathParam(r, key)
		if err != nil {
			return ""
		}
		return v
	}
	topic := param("topic")
	group := param("group")
	subject := param("subject")
	user := param("user")
	cluster := param("cluster")

	switch {
	// Clusters
	case strings.HasSuffix(pattern, "/clusters") && method == http.MethodGet:
		return "cluster", "", "view", ""
	case strings.HasSuffix(pattern, "/capabilities") && method == http.MethodGet:
		return "cluster", cluster, "view", ""
	case strings.HasSuffix(pattern, "/capabilities/refresh") && method == http.MethodPost:
		return "cluster", cluster, "view", ""
	case strings.HasSuffix(pattern, "/brokers") && method == http.MethodGet:
		return "cluster", cluster, "view", ""

	// Topics
	case strings.HasSuffix(pattern, "/topics") && method == http.MethodGet:
		return "topic", "", "view", ""
	case strings.HasSuffix(pattern, "/topics") && method == http.MethodPost:
		return "topic", "", "edit", "name"
	case strings.HasSuffix(pattern, "/topics/{topic}") && method == http.MethodGet:
		return "topic", topic, "view", ""
	case strings.HasSuffix(pattern, "/topics/{topic}") && method == http.MethodDelete:
		return "topic", topic, "delete", ""
	// The handler also drops the consumer groups the caller may not view.
	case strings.HasSuffix(pattern, "/topics/{topic}/consumers") && method == http.MethodGet:
		return "topic", topic, "view", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/configs") && method == http.MethodPatch:
		return "topic", topic, "edit", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/records") && method == http.MethodDelete:
		return "topic", topic, "delete", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/sample") && method == http.MethodGet:
		return "topic", topic, "consume", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages/count") && method == http.MethodGet:
		return "topic", topic, "consume", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages/timeline") && method == http.MethodGet:
		return "topic", topic, "consume", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages/{partition}/{offset}/raw") && method == http.MethodGet:
		return "topic", topic, "consume", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages") && method == http.MethodGet:
		return "topic", topic, "consume", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages") && method == http.MethodPost:
		return "topic", topic, "produce", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages/search") && method == http.MethodPost:
		return "topic", topic, "consume", ""
	// The destination side of a copy is an arbitrary cluster/topic named in
	// the request body, not the URL; copyMessages checks it explicitly with
	// its own "topic:produce" Allow() call, since resolvePermission only
	// ever authorizes against this route's {cluster}/{topic} (the source).
	case strings.HasSuffix(pattern, "/topics/{topic}/copy") && method == http.MethodPost:
		return "topic", topic, "consume", ""

	// Groups
	case strings.HasSuffix(pattern, "/groups") && method == http.MethodGet:
		return "group", "", "view", ""
	case strings.HasSuffix(pattern, "/groups") && method == http.MethodPost:
		return "group", "", "edit", "group_id"
	case strings.HasSuffix(pattern, "/groups/{group}") && method == http.MethodGet:
		return "group", group, "view", ""
	case strings.HasSuffix(pattern, "/groups/{group}") && method == http.MethodDelete:
		return "group", group, "delete", ""
	case strings.HasSuffix(pattern, "/groups/{group}/reset-offsets") && method == http.MethodPost:
		return "group", group, "edit", ""

	// Schemas
	case strings.HasSuffix(pattern, "/schemas/subjects") && method == http.MethodGet:
		return "schema", "", "view", ""
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}/versions") && method == http.MethodGet:
		return "schema", subject, "view", ""
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}/versions/{version}") && method == http.MethodGet:
		return "schema", subject, "view", ""
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}/versions") && method == http.MethodPost:
		return "schema", subject, "edit", ""
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}") && method == http.MethodDelete:
		return "schema", subject, "delete", ""

	// ACLs are cluster-scoped and have no resource name: any acl grant with
	// the action covers every ACL of the cluster.
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodGet:
		return "acl", "", "view", ""
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodPost:
		return "acl", "", "edit", ""
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodDelete:
		return "acl", "", "delete", ""

	// Users
	case strings.HasSuffix(pattern, "/users") && method == http.MethodGet:
		return "user", "", "view", ""
	case strings.HasSuffix(pattern, "/users") && method == http.MethodPost:
		return "user", "", "edit", "user"
	case strings.HasSuffix(pattern, "/users/{user}") && method == http.MethodDelete:
		return "user", user, "delete", ""
	}
	return "", "", "", ""
}
