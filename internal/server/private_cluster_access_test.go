// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
)

// privateAccessCase is a server configuration and the 403 code userMallory
// gets for private clusters under it, "" when they are allowed. Every case
// grants topic:consume when RBAC is enabled, which the copy source needs.
type privateAccessCase struct {
	name     string
	cfg      config.Config
	wantCode string
}

func withPrivateMode(cfg config.Config, mode config.PrivateClusterMode) config.Config {
	cfg.PrivateClusters.Mode = mode
	return cfg
}

func privateAccessCases() []privateAccessCase {
	consume := perm("topic:*", "consume")
	return []privateAccessCase{
		{"on", config.Defaults(), ""},
		{"on with rbac", withPrivateMode(rbacGrantConfig(consume), config.PrivateClustersOn), ""},
		{"off", withPrivateMode(config.Defaults(), config.PrivateClustersOff), privateClustersDisabledCode},
		{"off with every permission", withPrivateMode(rbacGrantConfig(perm("*", "*")), config.PrivateClustersOff), privateClustersDisabledCode},
		{"role without the permission", withPrivateMode(rbacGrantConfig(consume), config.PrivateClustersRole), privateClustersForbiddenCode},
		{"role with another action", withPrivateMode(rbacGrantConfig(consume, perm("*", "view")), config.PrivateClustersRole), privateClustersForbiddenCode},
		{"role with the permission", withPrivateMode(rbacGrantConfig(consume, perm("private_cluster", "use")), config.PrivateClustersRole), ""},
		{"role with every permission", withPrivateMode(rbacGrantConfig(perm("*", "*")), config.PrivateClustersRole), ""},
	}
}

// finishedCopyRegistry serves one page that ends the copy.
type finishedCopyRegistry struct{ *blockingCopyRegistry }

func (finishedCopyRegistry) ConsumeMessages(context.Context, string, string, kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error) {
	return &kafkapkg.ConsumeResult{Messages: []kafkapkg.Message{{Partition: 0, Offset: 0, Timestamp: 1, Value: "v", ValueEncoding: "text"}}}, nil
}

// privateAccessServer serves New with cfg, fakes that answer a topic list
// and Test connection for any private cluster, and a copy registry whose
// copies complete. The configured cluster "src" holds the copy source.
func privateAccessServer(t *testing.T, cfg config.Config, validator auth.Validator) http.Handler {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "src", Brokers: []string{unreachableBroker}}}, logger)
	t.Cleanup(reg.Close)
	copyReg := finishedCopyRegistry{newBlockingCopyRegistry(reg)}
	st := stores{
		clusters: fakeClusters{},
		topics:   fakeTopics{topics: []kafkapkg.TopicInfo{{Name: "orders"}}},
	}
	return New(Options{
		Version:      "v-test",
		Logger:       logger,
		Config:       cfg,
		Auth:         validator,
		stores:       &st,
		copyRegistry: copyReg,
	})
}

// privateRequest is a request for userMallory, with a JSON body when body
// is set and the X-Kafkito-Cluster header when header is set.
type privateRequest struct {
	name, method, path, body, header string
}

func (p privateRequest) send(h http.Handler, token string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(p.method, p.path, strings.NewReader(p.body))
	if p.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(rbacTestHeader, userMallory)
	if p.header != "" {
		req.Header.Set(PrivateClusterHeader, p.header)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return req, rec
}

const privateCopyBody = `{"dest_topic":"orders2","dest_cluster_config":{"brokers":["` + unreachableBroker + `"]}}`

// privateEntryPoints are the requests that use a private cluster. Each is
// answered with 200 when the caller may use private clusters.
func privateEntryPoints(t *testing.T) []privateRequest {
	header := encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
	return []privateRequest{
		{"list topics on __private__", http.MethodGet, "/api/v1/clusters/" + config.PrivateClusterSentinel + "/topics", "", header},
		{"test connection with a body", http.MethodPost, "/api/v1/clusters/_test", testClusterBody, ""},
		{"test connection with the header", http.MethodPost, "/api/v1/clusters/_test", "", header},
		{"copy into a dest_cluster_config", http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", privateCopyBody, ""},
	}
}

// assertPrivateClustersDenied checks the 403 of the gate: its code, a fixed
// message and the documented shape.
func assertPrivateClustersDenied(t *testing.T, req *http.Request, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	want := map[string]string{
		privateClustersDisabledCode:  "private clusters are disabled",
		privateClustersForbiddenCode: "your role does not allow private clusters",
	}[code]
	assert.JSONEq(t, `{"error":"`+want+`","code":"`+code+`"}`, rec.Body.String())
	assertResponseMatchesSpec(t, contractRouter(t), req, rec)
}

// Every request that uses a private cluster goes through
// private_clusters.mode: the __private__ path segment, Test connection and
// a copy into a dest_cluster_config. The copies run to completion, so this
// test is not parallel (see topic_copy_stream_test.go).
func TestPrivateClusterAccess_EntryPoints(t *testing.T) {
	for _, tc := range privateAccessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := privateAccessServer(t, tc.cfg, nil)
			for _, ep := range privateEntryPoints(t) {
				req, rec := ep.send(h, "")
				if tc.wantCode != "" {
					assertPrivateClustersDenied(t, req, rec, tc.wantCode)
					continue
				}
				require.Equal(t, http.StatusOK, rec.Code, "%s: %s", ep.name, rec.Body.String())
				assertResponseMatchesSpec(t, contractRouter(t), req, rec)
				if strings.HasSuffix(ep.path, "/copy") {
					assert.Contains(t, rec.Body.String(), `"done":true`, ep.name)
					assert.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond, "slot released")
				}
			}
		})
	}
}

// When the mode refuses the caller, the X-Kafkito-Cluster header is not
// read at all: a __private__ request gets the 403 whatever the header
// holds, where mode on rejects a malformed header with 400. The header on a
// request for a configured cluster is ignored.
func TestPrivateClusterAccess_DeniedCallerHeaderIsNotRead(t *testing.T) {
	t.Parallel()

	unresolvable := encodeHeader(t, config.ClusterConfig{Brokers: []string{"kafka.invalid:9092"}})
	blocked := encodeHeader(t, config.ClusterConfig{Brokers: []string{"127.0.0.1:9092"}})
	notJSON := base64.StdEncoding.EncodeToString([]byte("not json"))
	private := "/api/v1/clusters/" + config.PrivateClusterSentinel + "/topics"
	configured := "/api/v1/clusters/src/topics"

	for _, tc := range []struct {
		name string
		cfg  config.Config
		code string
	}{
		{"off", withPrivateMode(config.Defaults(), config.PrivateClustersOff), privateClustersDisabledCode},
		{"role without the permission", withPrivateMode(rbacGrantConfig(perm("topic:*", "view", "consume")), config.PrivateClustersRole), privateClustersForbiddenCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := privateAccessServer(t, tc.cfg, nil)
			for _, header := range []string{"", "%%%", notJSON, unresolvable, blocked} {
				req, rec := privateRequest{"list", http.MethodGet, private, "", header}.send(h, "")
				assertPrivateClustersDenied(t, req, rec, tc.code)
				req, rec = privateRequest{"test", http.MethodPost, "/api/v1/clusters/_test", "", header}.send(h, "")
				assertPrivateClustersDenied(t, req, rec, tc.code)
				req, rec = privateRequest{"copy", http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", `{"dest_topic":"orders2","dest_cluster_config":{"brokers":["127.0.0.1:9092"]}}`, header}.send(h, "")
				assertPrivateClustersDenied(t, req, rec, tc.code)

				if header == "" {
					continue
				}
				req, rec = privateRequest{"configured", http.MethodGet, configured, "", header}.send(h, "")
				require.Equal(t, http.StatusOK, rec.Code, "header %q: %s", header, rec.Body.String())
				assert.Contains(t, rec.Body.String(), `"cluster":"src"`)
				assertResponseMatchesSpec(t, contractRouter(t), req, rec)
			}
		})
	}

	t.Run("on", func(t *testing.T) {
		t.Parallel()
		h := privateAccessServer(t, config.Defaults(), nil)
		for _, path := range []string{private, configured} {
			_, rec := privateRequest{"malformed", http.MethodGet, path, "", "%%%"}.send(h, "")
			assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", path, rec.Body.String())
		}
	})
}

// In mode role the RBAC subject is the verified principal when there is
// one, as for every other permission, not the identity header.
func TestPrivateClusterAccess_RoleUsesThePrincipal(t *testing.T) {
	t.Parallel()

	grant := func(user string) config.Config {
		cfg := withPrivateMode(rbacGrantConfig(), config.PrivateClustersRole)
		cfg.RBAC.Roles = []config.RoleConfig{{Name: "private", Permissions: []config.PermissionConfig{perm("private_cluster", "use")}}}
		cfg.RBAC.Subjects = []config.SubjectConfig{{User: user, Roles: []string{"private"}}}
		return cfg
	}
	ep := privateEntryPoints(t)[0]

	// acceptingValidator authenticates everyone as userAdmin; the identity
	// header names userMallory.
	h := privateAccessServer(t, grant(userAdmin), acceptingValidator{})
	_, rec := ep.send(h, "token")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	h = privateAccessServer(t, grant(userMallory), acceptingValidator{})
	req, rec := ep.send(h, "token")
	assertPrivateClustersDenied(t, req, rec, privateClustersForbiddenCode)
}

// GET /api/v1/me reports the mode and whether the caller may use private
// clusters.
func TestPrivateClusterAccess_Me(t *testing.T) {
	t.Parallel()

	router := contractRouter(t)
	for _, tc := range privateAccessCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := privateAccessServer(t, tc.cfg, nil)
			req, rec := privateRequest{"me", http.MethodGet, "/api/v1/me", "", ""}.send(h, "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assertResponseMatchesSpec(t, router, req, rec)

			var body struct {
				PrivateClusters struct {
					Mode    string `json:"mode"`
					Allowed bool   `json:"allowed"`
				} `json:"private_clusters"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, string(tc.cfg.PrivateClusters.EffectiveMode()), body.PrivateClusters.Mode)
			assert.Equal(t, tc.wantCode == "", body.PrivateClusters.Allowed)
		})
	}
}

// The zero value allows private clusters; a mode config.Validate refuses
// counts as off, and mode role never allows them without an enabled policy.
func TestPrivateClusterAccess_Check(t *testing.T) {
	t.Parallel()

	grant := rbac.Compile(rbacGrantConfig(perm("private_cluster", "use")).RBAC)
	disabled := rbac.Compile(config.RBACConfig{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(rbacTestHeader, userMallory)

	for _, tc := range []struct {
		name   string
		access privateClusterAccess
		mode   config.PrivateClusterMode
		code   string
	}{
		{"zero value", privateClusterAccess{}, config.PrivateClustersOn, ""},
		{"mode in another case", privateClusterAccess{cfg: config.PrivateClustersConfig{Mode: " OFF "}}, config.PrivateClustersOff, privateClustersDisabledCode},
		{"unknown mode", privateClusterAccess{cfg: config.PrivateClustersConfig{Mode: "sometimes"}}, config.PrivateClustersOff, privateClustersDisabledCode},
		{"role with the permission", privateClusterAccess{cfg: config.PrivateClustersConfig{Mode: config.PrivateClustersRole}, policy: grant}, config.PrivateClustersRole, ""},
		{"role without a policy", privateClusterAccess{cfg: config.PrivateClustersConfig{Mode: config.PrivateClustersRole}}, config.PrivateClustersRole, privateClustersForbiddenCode},
		{"role with rbac disabled", privateClusterAccess{cfg: config.PrivateClustersConfig{Mode: config.PrivateClustersRole}, policy: disabled}, config.PrivateClustersRole, privateClustersForbiddenCode},
	} {
		assert.Equal(t, tc.mode, tc.access.mode(), tc.name)
		err := tc.access.check(req)
		if tc.code == "" {
			require.NoError(t, err, tc.name)
			continue
		}
		var apiErr *apiError
		require.ErrorAs(t, err, &apiErr, tc.name)
		assert.Equal(t, http.StatusForbidden, apiErr.Status, tc.name)
		assert.Equal(t, tc.code, apiErr.Code, tc.name)
	}
}
