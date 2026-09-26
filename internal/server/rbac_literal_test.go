// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// A resource named "*" is a concrete name: a prefix grant does not cover it,
// whether the "*" comes in literally or percent-encoded.
func TestRBAC_StarIsAConcreteName(t *testing.T) {
	t.Parallel()

	for _, k := range pathParamKinds {
		for _, tc := range []struct {
			name, resource, segment string
			wantStatus              int
		}{
			{"prefix grant, literal star", "team-*", "*", http.StatusForbidden},
			{"prefix grant, encoded star", "team-*", "%2A", http.StatusForbidden},
			{"prefix grant, encoded lower-case star", "team-*", "%2a", http.StatusForbidden},
			{"exact grant, literal star", "team-a", "*", http.StatusForbidden},
			{"prefix grant, matching name", "team-*", "team-a", http.StatusTeapot},
			{"wildcard grant, literal star", "*", "*", http.StatusTeapot},
			{"wildcard grant, encoded star", "*", "%2A", http.StatusTeapot},
		} {
			t.Run(k.field+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				var got boundParams
				h, _, _ := pathParamServer(t, k.resType+":"+tc.resource, &got)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, pathParamRequest(t, k.method, k.path, tc.segment))
				require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
				if tc.wantStatus == http.StatusForbidden {
					assert.Zero(t, got.calls, "handler must not run")
					assert.JSONEq(t, `{"error":"forbidden","resource":"`+k.resType+`:*","action":"`+rbacAction(k.method)+`"}`, rec.Body.String())
				}
			})
		}
	}
}

func rbacAction(method string) string {
	if method == http.MethodDelete {
		return "delete"
	}
	return "view"
}

// The literal and encoded star in the named destructive routes of the
// review: DELETE on a group named "*".
func TestRBAC_DeleteStarGroupNeedsMatchingGrant(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/api/v1/clusters/c/groups/*", "/api/v1/clusters/c/groups/%2A"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			var got boundParams
			h, _, _ := pathParamServer(t, "group:team-*", &got)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, pathParamRequest(t, http.MethodDelete, "%s", path))
			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Zero(t, got.calls)
		})
	}
}

// An empty path parameter is never the "any name" case. server.New cleans
// the path, so "//" collapses and no empty parameter is routed; the RBAC
// middleware rejects one where the router would deliver it.
func TestRBAC_EmptyPathParamIsNotAnyName(t *testing.T) {
	t.Parallel()

	t.Run("server cleans the path", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			method, path string
		}{
			{http.MethodGet, "/api/v1/clusters/c/groups//reset-offsets"},
			{http.MethodDelete, "/api/v1/clusters/c/groups/"},
			{http.MethodGet, "/api/v1/clusters//groups/x"},
			{http.MethodGet, "/api/v1/clusters/c/schemas/subjects//versions"},
			{http.MethodDelete, "/api/v1/clusters/c/users/"},
		} {
			var got boundParams
			h, _, _ := pathParamServer(t, "group:team-*", &got)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, pathParamRequest(t, tc.method, "%s", tc.path))
			assert.NotEqual(t, http.StatusTeapot, rec.Code, "%s %s: %s", tc.method, tc.path, rec.Body.String())
			for f, v := range got.values {
				assert.NotEmpty(t, v, "%s %s: bound %s", tc.method, tc.path, f)
			}
		}
	})

	t.Run("router that delivers an empty parameter", func(t *testing.T) {
		t.Parallel()
		next := false
		r := newRBACRouter(t, policyGroupGrant("group:team-*", "edit"), http.MethodPost,
			"/api/v1/clusters/{cluster}/groups/{group}/reset-offsets",
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) { next = true }))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c/groups//reset-offsets", strings.NewReader(`{}`))
		req.Header.Set(rbacTestHeader, userMallory)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"code":"invalid_request","error":"invalid parameter \"group\""}`, rec.Body.String())
		assert.False(t, next, "next must not run")
	})
}

// List results are real names and matched literally: a group named "*" is
// only visible to a grant that matches it.
func TestRBAC_ListHidesStarGroup(t *testing.T) {
	t.Parallel()

	c := newKfake(t, "orders")
	fetchOffsetsLikeKafka(c)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "kf", Brokers: []string{c.ListenAddrs()[0]}}}, slog.Default())
	t.Cleanup(reg.Close)
	cfg := config.Defaults()
	cfg.RBAC = config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
		Roles: []config.RoleConfig{
			{Name: "admin", Permissions: []config.PermissionConfig{{Resource: "*", Actions: []string{"*"}}}},
			{Name: "team", Permissions: []config.PermissionConfig{
				{Resource: "group:team-*", Actions: []string{"*"}},
				{Resource: "topic:*", Actions: []string{"view"}},
			}},
		},
		Subjects: []config.SubjectConfig{
			{User: "admin", Roles: []string{"admin"}},
			{User: userMallory, Roles: []string{"team"}},
		},
	}
	h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: cfg})

	do := func(user, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(rbacTestHeader, user)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, g := range []string{"*", "team-a", "other"} {
		rec := do("admin", http.MethodPost, "/api/v1/clusters/kf/groups", `{"group_id":"`+g+`","topic":"orders","strategy":"earliest"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	// Creating is checked on the body's name, also literally.
	rec := do(userMallory, http.MethodPost, "/api/v1/clusters/kf/groups", `{"group_id":"*","topic":"orders","strategy":"earliest"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	groupIDs := func(user, path, field string) []string {
		rec := do(user, http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		var items []map[string]any
		require.NoError(t, json.Unmarshal(body[field], &items), rec.Body.String())
		var ids []string
		for _, g := range items {
			ids = append(ids, g["group_id"].(string))
		}
		sort.Strings(ids)
		return ids
	}
	assert.Equal(t, []string{"*", "other", "team-a"}, groupIDs("admin", "/api/v1/clusters/kf/groups", "groups"))
	assert.Equal(t, []string{"team-a"}, groupIDs(userMallory, "/api/v1/clusters/kf/groups", "groups"))
	assert.Equal(t, []string{"*", "other", "team-a"}, groupIDs("admin", "/api/v1/clusters/kf/topics/orders/consumers", "consumers"))
	assert.Equal(t, []string{"team-a"}, groupIDs(userMallory, "/api/v1/clusters/kf/topics/orders/consumers", "consumers"))

	for _, path := range []string{"/api/v1/clusters/kf/groups/*", "/api/v1/clusters/kf/groups/%2A"} {
		assert.Equal(t, http.StatusForbidden, do(userMallory, http.MethodGet, path, "").Code, path)
		assert.Equal(t, http.StatusForbidden, do(userMallory, http.MethodDelete, path, "").Code, path)
	}
	assert.Equal(t, http.StatusOK, do("admin", http.MethodGet, "/api/v1/clusters/kf/groups/%2A", "").Code)
}

// The list filters match real names literally, an empty group id included.
func TestFilterGroups_NamesAreLiteral(t *testing.T) {
	t.Parallel()

	policy := policyGroupGrant("group:team-*", "view")
	groups := []kafkapkg.GroupInfo{{GroupID: "*"}, {GroupID: ""}, {GroupID: "team-a"}, {GroupID: "other"}}
	var ids []string
	for _, g := range filterGroupsByRBAC(groups, policy, userMallory, "c") {
		ids = append(ids, g.GroupID)
	}
	assert.Equal(t, []string{"team-a"}, ids)

	consumers := []kafkapkg.TopicConsumer{{GroupID: "*"}, {GroupID: ""}, {GroupID: "team-a"}}
	ids = nil
	for _, c := range filterTopicConsumersByRBAC(consumers, policy, userMallory, "c") {
		ids = append(ids, c.GroupID)
	}
	assert.Equal(t, []string{"team-a"}, ids)

	all := policyGroupGrant("group:*", "view")
	assert.Len(t, filterGroupsByRBAC(groups, all, userMallory, "c"), len(groups))
}
