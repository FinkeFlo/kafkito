// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/FinkeFlo/kafkito/pkg/rbac"
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

// rbacMiddleware enforces RBAC for cluster routes. The identity is resolved
// from the configured header; the resource/action is derived from the matched
// chi route pattern and HTTP method.
func rbacMiddleware(policy *rbac.Policy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := rbacSubject(r, policy)
			r = r.WithContext(withSubject(r.Context(), user))

			if !policy.Enabled() {
				next.ServeHTTP(w, r)
				return
			}

			resType, resName, action, bodyField := resolvePermission(r)
			if resType == "" {
				next.ServeHTTP(w, r)
				return
			}

			if bodyField != "" {
				bodyBytes, err := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
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

			cluster := chi.URLParam(r, "cluster")
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

// resolvePermission maps the current request to (resourceType, resourceName,
// action, bodyField). A non-empty bodyField names the JSON request-body field
// that holds the resource name; the middleware reads it to derive resName
// (e.g. POST /topics uses "name", POST /groups uses "group_id"). An empty
// bodyField means resName is already final. A return of ("", "", "", "")
// means no permission check is required.
func resolvePermission(r *http.Request) (resType, resName, action, bodyField string) {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return
	}
	method := r.Method
	pattern := rctx.RoutePattern()

	topic := chi.URLParam(r, "topic")
	group := chi.URLParam(r, "group")
	subject := chi.URLParam(r, "subject")
	user := chi.URLParam(r, "user")
	cluster := chi.URLParam(r, "cluster")

	switch {
	// Clusters
	case strings.HasSuffix(pattern, "/clusters") && method == http.MethodGet:
		return "cluster", "*", "view", ""
	case strings.HasSuffix(pattern, "/capabilities") && method == http.MethodGet:
		return "cluster", cluster, "view", ""
	case strings.HasSuffix(pattern, "/capabilities/refresh") && method == http.MethodPost:
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
	case strings.HasSuffix(pattern, "/topics/{topic}/messages") && method == http.MethodGet:
		return "topic", topic, "consume", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages") && method == http.MethodPost:
		return "topic", topic, "produce", ""
	case strings.HasSuffix(pattern, "/topics/{topic}/messages/search") && method == http.MethodPost:
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

	// ACLs
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodGet:
		return "acl", "*", "view", ""
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodPost:
		return "acl", "*", "edit", ""
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodDelete:
		return "acl", "*", "delete", ""

	// Users
	case strings.HasSuffix(pattern, "/users") && method == http.MethodGet:
		return "user", "", "view", ""
	case strings.HasSuffix(pattern, "/users") && method == http.MethodPost:
		return "user", "", "edit", ""
	case strings.HasSuffix(pattern, "/users/{user}") && method == http.MethodDelete:
		return "user", user, "delete", ""
	}
	return "", "", "", ""
}

// handleMe returns the current principal, roles and materialized permissions.
func handleMe(policy *rbac.Policy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Prefer the JWT-derived principal; fall back to legacy header trust for compatibility.
		var (
			user   string
			email  string
			scopes []string
			tenant string
			hasJWT bool
		)
		if p, ok := auth.PrincipalFromContext(r.Context()); ok {
			hasJWT = true
			email = p.Email
			scopes = p.Scopes
			tenant = p.Tenant
		}
		// rbacSubject is the single identity resolver: principal first, header fallback.
		user = rbacSubject(r, policy)
		writeJSON(w, http.StatusOK, map[string]any{
			"user":         user,
			"email":        email,
			"tenant":       tenant,
			"scopes":       scopes,
			"roles":        policy.ResolveRoles(user),
			"permissions":  policy.MaterializePermissions(user),
			"anonymous":    user == "",
			"jwt":          hasJWT,
			"rbac_enabled": policy.Enabled(),
		})
	}
}
