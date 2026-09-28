// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// fakeAdhocClusters resolves every private cluster to one ad-hoc name.
type fakeAdhocClusters struct{ clusterStore }

func (fakeAdhocClusters) UseAdhoc(config.ClusterConfig) (string, error) {
	return kafkapkg.AdhocPrefix + "test", nil
}

func (fakeAdhocClusters) ListBrokers(context.Context, string) ([]kafkapkg.BrokerInfo, error) {
	return []kafkapkg.BrokerInfo{{NodeID: 1, Host: "broker-1", Port: 9092, IsController: true}}, nil
}

// rbacGrantConfig enables RBAC and grants userMallory perms.
func rbacGrantConfig(perms ...config.PermissionConfig) config.Config {
	cfg := config.Defaults()
	cfg.RBAC = config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
		Roles:    []config.RoleConfig{{Name: "scoped", Permissions: perms}},
		Subjects: []config.SubjectConfig{{User: userMallory, Roles: []string{"scoped"}}},
	}
	return cfg
}

func perm(resource string, actions ...string) config.PermissionConfig {
	return config.PermissionConfig{Resource: resource, Actions: actions}
}

// rbacFakeServer serves New with cfg and st in place of a registry.
func rbacFakeServer(t *testing.T, cfg config.Config, st stores) http.Handler {
	t.Helper()
	st.clusters = fakeAdhocClusters{}
	return New(Options{
		Version: "v-test",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config:  cfg,
		stores:  &st,
	})
}

// rbacListCase is one list request of userMallory and the names it must
// return.
type rbacListCase struct {
	name    string
	cfg     config.Config
	private bool
	want    []string
}

// rbacListCases are the grant shapes every filtered list is checked with:
// all is the full, sorted list, exact one name in it and teams its names
// starting with "team-".
func rbacListCases(resType string, all []string, exact string, teams []string) []rbacListCase {
	return []rbacListCase{
		{name: "exact", cfg: rbacGrantConfig(perm(resType+":"+exact, "view")), want: []string{exact}},
		{name: "glob", cfg: rbacGrantConfig(perm(resType+":team-*", "view")), want: teams},
		{name: "exact and glob", cfg: rbacGrantConfig(perm(resType+":"+exact, "view"), perm(resType+":team-*", "view")), want: append([]string{exact}, teams...)},
		{name: "no match", cfg: rbacGrantConfig(perm(resType+":none-*", "view")), want: []string{}},
		{name: "type wildcard", cfg: rbacGrantConfig(perm(resType+":*", "view")), want: all},
		{name: "type without name", cfg: rbacGrantConfig(perm(resType, "view")), want: all},
		{name: "resource wildcard", cfg: rbacGrantConfig(perm("*", "*")), want: all},
		{name: "wildcard for another action", cfg: rbacGrantConfig(perm(resType+":*", "edit"), perm(resType+":"+exact, "view")), want: []string{exact}},
		{name: "rbac disabled", cfg: config.Defaults(), want: all},
		// RBAC does not apply to private clusters, so their lists stay whole.
		{name: "private cluster", cfg: rbacGrantConfig(perm(resType+":"+exact, "view")), private: true, want: all},
	}
}

// listNames sends GET path as userMallory and returns the value of field
// of every item in the list under key.
func listNames(t *testing.T, h http.Handler, tc rbacListCase, path, key, field string) []string {
	t.Helper()
	cluster := "kf"
	header := map[string]string{rbacTestHeader: userMallory}
	if tc.private {
		cluster = config.PrivateClusterSentinel
		header[PrivateClusterHeader] = encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
	}
	req, rec := requestCase{method: http.MethodGet, path: "/api/v1/clusters/" + cluster + path, header: header}.do(t, h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assertResponseMatchesSpec(t, contractRouter(t), req, rec)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	var items []map[string]any
	require.NoError(t, json.Unmarshal(body[key], &items), rec.Body.String())
	require.NotNil(t, items, "an empty list is [], not null: %s", rec.Body.String())
	names := []string{}
	for _, it := range items {
		names = append(names, it[field].(string))
	}
	return names
}

// The subjects list only returns the subjects the caller may view.
func TestListSubjects_RBACFilter(t *testing.T) {
	t.Parallel()

	all := []string{"*", "orders-key", "orders-value", "payments-value", "team-a-value", "team-b-value"}
	subjects := make([]kafkapkg.Subject, 0, len(all))
	for _, n := range all {
		subjects = append(subjects, kafkapkg.Subject{Name: n, Versions: []int{1}})
	}
	st := stores{schemas: fakeSchemas{client: fakeSchemaClient{subjects: subjects}}}

	for _, tc := range rbacListCases("schema", all, "orders-value", []string{"team-a-value", "team-b-value"}) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := rbacFakeServer(t, tc.cfg, st)
			assert.Equal(t, tc.want, listNames(t, h, tc, "/schemas/subjects", "subjects", "name"))
		})
	}
}

// The SCRAM user list only returns the users the caller may view.
func TestListScramUsers_RBACFilter(t *testing.T) {
	t.Parallel()

	all := []string{"*", "alice", "bob", "team-a", "team-b"}
	users := make([]kafkapkg.SCRAMUser, 0, len(all))
	for _, n := range all {
		users = append(users, kafkapkg.SCRAMUser{User: n, Credentials: []kafkapkg.SCRAMCredential{{Mechanism: "SCRAM-SHA-256", Iterations: 8192}}})
	}
	st := stores{scram: fakeSCRAM{users: users}}

	for _, tc := range rbacListCases("user", all, "alice", []string{"team-a", "team-b"}) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := rbacFakeServer(t, tc.cfg, st)
			assert.Equal(t, tc.want, listNames(t, h, tc, "/users", "users", "user"))
		})
	}
}

// Creating or rotating a SCRAM user is checked against the user named in
// the body, exactly as the broker stores it.
func TestUpsertScramUser_RBACChecksBodyName(t *testing.T) {
	t.Parallel()

	const (
		ok          = `{"mechanism":"SCRAM-SHA-256","ok":true,"user":"%s"}`
		handlerDeny = `{"code":"rbac_denied","error":"forbidden"}`
	)
	deny := func(user string) string {
		return `{"action":"edit","error":"forbidden","resource":"user:` + user + `"}`
	}
	exact := rbacGrantConfig(perm("user:alice", "edit"))
	glob := rbacGrantConfig(perm("user:team-*", "edit"))

	for _, tc := range []struct {
		name       string
		cfg        config.Config
		private    bool
		body       string
		wantStatus int
		wantBody   string
	}{
		{name: "exact grant, same name", cfg: exact, body: scramBody("alice"), wantStatus: http.StatusOK, wantBody: strings.Replace(ok, "%s", "alice", 1)},
		{name: "exact grant, other name", cfg: exact, body: scramBody("bob"), wantStatus: http.StatusForbidden, wantBody: deny("bob")},
		{name: "exact grant, star name", cfg: exact, body: scramBody("*"), wantStatus: http.StatusForbidden, wantBody: deny("*")},
		{name: "exact grant, trailing space", cfg: exact, body: scramBody("alice "), wantStatus: http.StatusForbidden, wantBody: handlerDeny},
		{name: "exact grant, leading space", cfg: exact, body: scramBody(" alice"), wantStatus: http.StatusForbidden, wantBody: handlerDeny},
		{name: "glob grant, matching name", cfg: glob, body: scramBody("team-x"), wantStatus: http.StatusOK, wantBody: strings.Replace(ok, "%s", "team-x", 1)},
		{name: "glob grant, other name", cfg: glob, body: scramBody("alice"), wantStatus: http.StatusForbidden, wantBody: deny("alice")},
		{name: "view only", cfg: rbacGrantConfig(perm("user:*", "view")), body: scramBody("alice"), wantStatus: http.StatusForbidden, wantBody: deny("alice")},
		{name: "wildcard grant, padded name", cfg: rbacGrantConfig(perm("user:*", "edit")), body: scramBody("alice "), wantStatus: http.StatusOK, wantBody: strings.Replace(ok, "%s", "alice ", 1)},
		{name: "missing user", cfg: exact, body: `{"mechanism":"SCRAM-SHA-256","password":"pw-secret"}`, wantStatus: http.StatusBadRequest, wantBody: `{"error":"missing or invalid 'user' in request body"}`},
		{name: "rbac disabled", cfg: config.Defaults(), body: scramBody("bob"), wantStatus: http.StatusOK, wantBody: strings.Replace(ok, "%s", "bob", 1)},
		{name: "private cluster", cfg: exact, private: true, body: scramBody("bob"), wantStatus: http.StatusOK, wantBody: strings.Replace(ok, "%s", "bob", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var called []string
			h := rbacFakeServer(t, tc.cfg, stores{scram: fakeSCRAM{upsert: func(user, _, _ string, _ int32) error {
				called = append(called, user)
				return nil
			}}})
			cluster := "kf"
			header := map[string]string{rbacTestHeader: userMallory}
			if tc.private {
				cluster = config.PrivateClusterSentinel
				header[PrivateClusterHeader] = encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
			}
			req, rec := requestCase{
				method: http.MethodPost, path: "/api/v1/clusters/" + cluster + "/users",
				contentType: "application/json", header: header, body: tc.body,
			}.do(t, h)
			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.JSONEq(t, tc.wantBody, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), "pw-secret")
			if tc.wantStatus == http.StatusOK {
				assertResponseMatchesSpec(t, contractRouter(t), req, rec)
				assert.Len(t, called, 1)
			} else {
				assert.Empty(t, called, "the upsert must not reach the broker")
			}
		})
	}
}

// With RBAC on, the middleware reads the SCRAM upsert body to find the user
// before the route's limit applies. That read is capped at the route's own
// 16 KiB and answers with the route's message.
func TestUpsertScramUser_RBACBodyRead(t *testing.T) {
	t.Parallel()

	h := rbacFakeServer(t, rbacGrantConfig(perm("user:limit", "edit")), stores{scram: fakeSCRAM{upsert: func(string, string, string, int32) error { return nil }}})
	user := map[string]string{rbacTestHeader: userMallory}
	const path = "/api/v1/clusters/kf/users"
	body := scramBody("limit")

	rec := sendBody(h, http.MethodPost, path, strings.NewReader(padJSON(t, body, maxSCRAMBodyBytes)), user)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = sendBody(h, http.MethodPost, path, strings.NewReader(`{"user":"limit","mechanism":"SCRAM-SHA-256","password":"pw-secret","bogus":1}`), user)
	assert.Equal(t, http.StatusOK, rec.Code, "the handler sees the body the middleware read: %s", rec.Body.String())

	rec = sendBody(h, http.MethodPost, path, strings.NewReader(padJSON(t, body, maxSCRAMBodyBytes+1)), user)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"invalid json: http: request body too large"}`, rec.Body.String())

	src := &countingReader{}
	rec = sendBody(h, http.MethodPost, path, src, user)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.LessOrEqual(t, src.n, int64(maxSCRAMBodyBytes)+64<<10, "read %d bytes", src.n)
}

func scramBody(user string) string {
	b, _ := json.Marshal(map[string]string{"user": user, "mechanism": "SCRAM-SHA-256", "password": "pw-secret"})
	return string(b)
}
