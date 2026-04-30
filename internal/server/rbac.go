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

// rbacMiddleware enforces RBAC for cluster routes. The identity is resolved
// from the configured header; the resource/action is derived from the matched
// chi route pattern and HTTP method.
func rbacMiddleware(policy *rbac.Policy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := r.Header.Get(policy.Header())
			r = r.WithContext(withSubject(r.Context(), user))

			if !policy.Enabled() {
				next.ServeHTTP(w, r)
				return
			}

			resType, resName, action, readBody := resolvePermission(r)
			if resType == "" {
				next.ServeHTTP(w, r)
				return
			}

			if readBody {
				bodyBytes, err := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
					return
				}
				var tmp struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(bodyBytes, &tmp); err != nil || tmp.Name == "" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid 'name' in request body"})
					return
				}
				resName = tmp.Name
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
// action, readBody). readBody=true indicates that the request body must be
// inspected to derive the resource name (e.g. POST /topics with {"name":...}).
// A return of ("", "", "", false) means no permission check is required.
func resolvePermission(r *http.Request) (resType, resName, action string, readBody bool) {
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
		return "cluster", "*", "view", false
	case strings.HasSuffix(pattern, "/capabilities") && method == http.MethodGet:
		return "cluster", cluster, "view", false
	case strings.HasSuffix(pattern, "/capabilities/refresh") && method == http.MethodPost:
		return "cluster", cluster, "view", false

	// Topics
	case strings.HasSuffix(pattern, "/topics") && method == http.MethodGet:
		return "topic", "", "view", false
	case strings.HasSuffix(pattern, "/topics") && method == http.MethodPost:
		return "topic", "", "edit", true
	case strings.HasSuffix(pattern, "/topics/{topic}") && method == http.MethodGet:
		return "topic", topic, "view", false
	case strings.HasSuffix(pattern, "/topics/{topic}") && method == http.MethodDelete:
		return "topic", topic, "delete", false
	case strings.HasSuffix(pattern, "/topics/{topic}/configs") && method == http.MethodPatch:
		return "topic", topic, "edit", false
	case strings.HasSuffix(pattern, "/topics/{topic}/records") && method == http.MethodDelete:
		return "topic", topic, "delete", false
	case strings.HasSuffix(pattern, "/topics/{topic}/sample") && method == http.MethodGet:
		return "topic", topic, "consume", false
	case strings.HasSuffix(pattern, "/topics/{topic}/messages") && method == http.MethodGet:
		return "topic", topic, "consume", false
	case strings.HasSuffix(pattern, "/topics/{topic}/messages") && method == http.MethodPost:
		return "topic", topic, "produce", false
	case strings.HasSuffix(pattern, "/topics/{topic}/messages/search") && method == http.MethodPost:
		return "topic", topic, "consume", false

	// Groups
	case strings.HasSuffix(pattern, "/groups") && method == http.MethodGet:
		return "group", "", "view", false
	case strings.HasSuffix(pattern, "/groups/{group}") && method == http.MethodGet:
		return "group", group, "view", false
	case strings.HasSuffix(pattern, "/groups/{group}") && method == http.MethodDelete:
		return "group", group, "delete", false
	case strings.HasSuffix(pattern, "/groups/{group}/reset-offsets") && method == http.MethodPost:
		return "group", group, "edit", false

	// Schemas
	case strings.HasSuffix(pattern, "/schemas/subjects") && method == http.MethodGet:
		return "schema", "", "view", false
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}/versions") && method == http.MethodGet:
		return "schema", subject, "view", false
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}/versions/{version}") && method == http.MethodGet:
		return "schema", subject, "view", false
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}/versions") && method == http.MethodPost:
		return "schema", subject, "edit", false
	case strings.HasSuffix(pattern, "/schemas/subjects/{subject}") && method == http.MethodDelete:
		return "schema", subject, "delete", false

	// ACLs
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodGet:
		return "acl", "*", "view", false
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodPost:
		return "acl", "*", "edit", false
	case strings.HasSuffix(pattern, "/acls") && method == http.MethodDelete:
		return "acl", "*", "delete", false

	// Users
	case strings.HasSuffix(pattern, "/users") && method == http.MethodGet:
		return "user", "", "view", false
	case strings.HasSuffix(pattern, "/users") && method == http.MethodPost:
		return "user", "", "edit", false
	case strings.HasSuffix(pattern, "/users/{user}") && method == http.MethodDelete:
		return "user", user, "delete", false
	}
	return "", "", "", false
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
			user = p.Subject
			if p.UserName != "" {
				user = p.UserName
			}
			email = p.Email
			scopes = p.Scopes
			tenant = p.Tenant
		} else {
			user = r.Header.Get(policy.Header())
		}
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
