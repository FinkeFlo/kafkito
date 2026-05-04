// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/FinkeFlo/kafkito/pkg/rbac"
)

const (
	rbacTestHeader = "X-Test-User"

	userAdmin   = "alice"
	userMallory = "mallory"

	clusterShared = "myc"

	roleAdmin = "admin"
)

func policyDisabled() *rbac.Policy {
	return rbac.Compile(config.RBACConfig{Enabled: false})
}

func policyAllowAll() *rbac.Policy {
	return rbac.Compile(config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
		Roles: []config.RoleConfig{
			{
				Name: roleAdmin,
				Permissions: []config.PermissionConfig{
					{Resource: "*", Actions: []string{"*"}},
				},
			},
		},
		Subjects: []config.SubjectConfig{
			{User: userAdmin, Roles: []string{roleAdmin}},
		},
	})
}

func policyDeny() *rbac.Policy {
	return rbac.Compile(config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
	})
}

// errReader fails on Read so the rbacMiddleware body-read branch fires.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// newRBACRouter mounts the middleware in the same shape production uses
// (server.go: v1.Group with rbacMiddleware, then leaf routes registered
// directly on the same router). That way chi.RouteContext().RoutePattern()
// is the full leaf pattern when the middleware runs, which is what
// resolvePermission's switch reads.
func newRBACRouter(t *testing.T, policy *rbac.Policy, method, pattern string, h http.Handler) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.Group(func(g chi.Router) {
		g.Use(rbacMiddleware(policy))
		g.Method(method, pattern, h)
	})
	return r
}

func TestRBACMiddleware_DispatchBranches(t *testing.T) {
	t.Parallel()

	type row struct {
		name           string
		policy         *rbac.Policy
		method         string
		pattern        string
		urlPath        string
		body           io.Reader
		headerUser     string
		wantStatus     int
		wantNextCalled bool
		wantErrSubstr  string
		wantBody       map[string]any
	}

	cases := []row{
		{
			name:           "disabled_rbac_passes_through_without_any_check",
			policy:         policyDisabled(),
			method:         http.MethodGet,
			pattern:        "/api/v1/clusters/{cluster}/topics",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/topics",
			headerUser:     userMallory,
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
		{
			name:           "unmapped_pattern_passes_through_without_auth",
			policy:         policyDeny(),
			method:         http.MethodGet,
			pattern:        "/api/v1/clusters/{cluster}/unmapped",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/unmapped",
			headerUser:     userMallory,
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
		{
			name:           "body_read_error_returns_400",
			policy:         policyAllowAll(),
			method:         http.MethodPost,
			pattern:        "/api/v1/clusters/{cluster}/topics",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/topics",
			body:           errReader{},
			headerUser:     userAdmin,
			wantStatus:     http.StatusBadRequest,
			wantNextCalled: false,
			wantErrSubstr:  "failed to read request body",
		},
		{
			name:           "invalid_json_body_returns_400",
			policy:         policyAllowAll(),
			method:         http.MethodPost,
			pattern:        "/api/v1/clusters/{cluster}/topics",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/topics",
			body:           strings.NewReader("{not json"),
			headerUser:     userAdmin,
			wantStatus:     http.StatusBadRequest,
			wantNextCalled: false,
			wantErrSubstr:  "missing or invalid 'name' in request body",
		},
		{
			name:           "empty_name_in_body_returns_400",
			policy:         policyAllowAll(),
			method:         http.MethodPost,
			pattern:        "/api/v1/clusters/{cluster}/topics",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/topics",
			body:           strings.NewReader(`{"name":""}`),
			headerUser:     userAdmin,
			wantStatus:     http.StatusBadRequest,
			wantNextCalled: false,
			wantErrSubstr:  "missing or invalid 'name' in request body",
		},
		{
			name:           "valid_body_name_admin_allowed_passes_through",
			policy:         policyAllowAll(),
			method:         http.MethodPost,
			pattern:        "/api/v1/clusters/{cluster}/topics",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/topics",
			body:           strings.NewReader(`{"name":"orders"}`),
			headerUser:     userAdmin,
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
		{
			name:           "private_cluster_sentinel_bypasses_deny_policy",
			policy:         policyDeny(),
			method:         http.MethodDelete,
			pattern:        "/api/v1/clusters/{cluster}/topics/{topic}",
			urlPath:        "/api/v1/clusters/" + config.PrivateClusterSentinel + "/topics/orders",
			headerUser:     userMallory,
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
		{
			name:           "deny_policy_returns_403_with_resource_action_body",
			policy:         policyDeny(),
			method:         http.MethodDelete,
			pattern:        "/api/v1/clusters/{cluster}/topics/{topic}",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/topics/orders",
			headerUser:     userMallory,
			wantStatus:     http.StatusForbidden,
			wantNextCalled: false,
			wantBody: map[string]any{
				"error":    "forbidden",
				"resource": "topic:orders",
				"action":   "delete",
			},
		},
		{
			name:           "allow_returns_true_passes_through",
			policy:         policyAllowAll(),
			method:         http.MethodDelete,
			pattern:        "/api/v1/clusters/{cluster}/groups/{group}",
			urlPath:        "/api/v1/clusters/" + clusterShared + "/groups/billing",
			headerUser:     userAdmin,
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var nextCalled bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusNoContent)
			})
			r := newRBACRouter(t, tc.policy, tc.method, tc.pattern, next)
			req := httptest.NewRequest(tc.method, tc.urlPath, tc.body)
			req.Header.Set(rbacTestHeader, tc.headerUser)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code, "body=%s", rec.Body.String())
			assert.Equal(t, tc.wantNextCalled, nextCalled, "next-called expectation violated")
			if tc.wantErrSubstr != "" {
				var body map[string]string
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "raw=%s", rec.Body.String())
				assert.Equal(t, tc.wantErrSubstr, body["error"])
			}
			if tc.wantBody != nil {
				var body map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "raw=%s", rec.Body.String())
				for k, v := range tc.wantBody {
					assert.Equal(t, v, body[k], "field %q mismatch", k)
				}
			}
		})
	}
}

func TestRBACMiddleware_BodyEchoIsPreservedForDownstreamHandler(t *testing.T) {
	t.Parallel()

	const payload = `{"name":"orders"}`
	var seen []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		seen = buf
		w.WriteHeader(http.StatusNoContent)
	})
	r := newRBACRouter(t, policyAllowAll(), http.MethodPost,
		"/api/v1/clusters/{cluster}/topics", next)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/clusters/"+clusterShared+"/topics", bytes.NewBufferString(payload))
	req.Header.Set(rbacTestHeader, userAdmin)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, payload, string(seen),
		"middleware must re-wrap the consumed body so downstream still sees it")
}

func TestResolvePermission_RepresentativeEntries(t *testing.T) {
	t.Parallel()

	type want struct {
		resType  string
		resName  string
		action   string
		readBody bool
	}
	cases := []struct {
		name    string
		method  string
		pattern string
		urlPath string
		want    want
	}{
		{
			name:    "topics_post_marks_readBody_with_empty_name",
			method:  http.MethodPost,
			pattern: "/api/v1/clusters/{cluster}/topics",
			urlPath: "/api/v1/clusters/" + clusterShared + "/topics",
			want:    want{resType: "topic", resName: "", action: "edit", readBody: true},
		},
		{
			name:    "groups_delete_uses_url_param_for_resName",
			method:  http.MethodDelete,
			pattern: "/api/v1/clusters/{cluster}/groups/{group}",
			urlPath: "/api/v1/clusters/" + clusterShared + "/groups/billing",
			want:    want{resType: "group", resName: "billing", action: "delete", readBody: false},
		},
		{
			name:    "schemas_subjects_versions_post_uses_subject_url_param",
			method:  http.MethodPost,
			pattern: "/api/v1/clusters/{cluster}/schemas/subjects/{subject}/versions",
			urlPath: "/api/v1/clusters/" + clusterShared + "/schemas/subjects/orders-value/versions",
			want:    want{resType: "schema", resName: "orders-value", action: "edit", readBody: false},
		},
		{
			name:    "acls_post_uses_wildcard_resName",
			method:  http.MethodPost,
			pattern: "/api/v1/clusters/{cluster}/acls",
			urlPath: "/api/v1/clusters/" + clusterShared + "/acls",
			want:    want{resType: "acl", resName: "*", action: "edit", readBody: false},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got want
			capture := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				rt, rn, a, rb := resolvePermission(r)
				got = want{resType: rt, resName: rn, action: a, readBody: rb}
			})
			r := chi.NewRouter()
			r.Method(tc.method, tc.pattern, capture)
			req := httptest.NewRequest(tc.method, tc.urlPath, nil)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolvePermission_ReturnsEmpty_WhenNoChiRouteContext(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/anywhere", nil)

	rt, rn, a, rb := resolvePermission(req)

	assert.Empty(t, rt)
	assert.Empty(t, rn)
	assert.Empty(t, a)
	assert.False(t, rb)
}
