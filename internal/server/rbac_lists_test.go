// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
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
