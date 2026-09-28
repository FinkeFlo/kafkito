// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// rbacRequest sends GET path as userMallory to cluster, or to a private
// cluster.
func rbacRequest(t *testing.T, h http.Handler, private bool, path string) (int, string) {
	t.Helper()
	cluster := "kf"
	header := map[string]string{rbacTestHeader: userMallory}
	if private {
		cluster = config.PrivateClusterSentinel
		header[PrivateClusterHeader] = encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
	}
	_, rec := requestCase{method: http.MethodGet, path: "/api/v1/clusters/" + cluster + path, header: header}.do(t, h)
	return rec.Code, rec.Body.String()
}

// The raw download of a record needs topic:consume on the topic in the path.
func TestDownloadMessageRaw_RBAC(t *testing.T) {
	t.Parallel()

	const path = "/topics/orders/messages/0/5/raw"
	for _, tc := range []struct {
		name       string
		cfg        config.Config
		private    bool
		wantStatus int
		wantBody   string
	}{
		{name: "consume on the topic", cfg: rbacGrantConfig(perm("topic:orders", "consume")), wantStatus: http.StatusOK, wantBody: `{"id":5}`},
		{name: "consume on a glob", cfg: rbacGrantConfig(perm("topic:ord*", "consume")), wantStatus: http.StatusOK, wantBody: `{"id":5}`},
		{name: "consume on another topic", cfg: rbacGrantConfig(perm("topic:payments", "consume")), wantStatus: http.StatusForbidden, wantBody: `{"action":"consume","error":"forbidden","resource":"topic:orders"}`},
		{name: "view only", cfg: rbacGrantConfig(perm("topic:*", "view")), wantStatus: http.StatusForbidden, wantBody: `{"action":"consume","error":"forbidden","resource":"topic:orders"}`},
		{name: "no grant", cfg: rbacGrantConfig(), wantStatus: http.StatusForbidden, wantBody: `{"action":"consume","error":"forbidden","resource":"topic:orders"}`},
		{name: "rbac disabled", cfg: config.Defaults(), wantStatus: http.StatusOK, wantBody: `{"id":5}`},
		{name: "private cluster", cfg: rbacGrantConfig(), private: true, wantStatus: http.StatusOK, wantBody: `{"id":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fetched := 0
			h := rbacFakeServer(t, tc.cfg, stores{messages: fakeMessages{raw: func(topic string, partition int32, offset int64) (*kafkapkg.RawMessageValue, error) {
				fetched++
				assert.Equal(t, "orders", topic)
				assert.EqualValues(t, 0, partition)
				assert.EqualValues(t, 5, offset)
				return &kafkapkg.RawMessageValue{Value: []byte(`{"id":5}`), ContentType: "application/json", Extension: "json"}, nil
			}}})
			status, body := rbacRequest(t, h, tc.private, path)
			require.Equal(t, tc.wantStatus, status, body)
			if tc.wantStatus == http.StatusOK {
				assert.Equal(t, tc.wantBody, body)
				assert.Equal(t, 1, fetched)
			} else {
				assert.JSONEq(t, tc.wantBody, body)
				assert.Zero(t, fetched, "a denied download must not read the record")
			}
		})
	}
}

// The broker list needs cluster:view on the cluster, like its capabilities.
func TestListBrokers_RBAC(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		cfg        config.Config
		private    bool
		wantStatus int
	}{
		{name: "view on the cluster", cfg: rbacGrantConfig(perm("cluster:kf", "view")), wantStatus: http.StatusOK},
		{name: "view on every cluster", cfg: rbacGrantConfig(perm("cluster:*", "view")), wantStatus: http.StatusOK},
		{name: "view on another cluster", cfg: rbacGrantConfig(perm("cluster:other", "view")), wantStatus: http.StatusForbidden},
		{name: "topic grant only", cfg: rbacGrantConfig(perm("topic:*", "*")), wantStatus: http.StatusForbidden},
		{name: "rbac disabled", cfg: config.Defaults(), wantStatus: http.StatusOK},
		{name: "private cluster", cfg: rbacGrantConfig(), private: true, wantStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := rbacFakeServer(t, tc.cfg, stores{})
			status, body := rbacRequest(t, h, tc.private, "/brokers")
			require.Equal(t, tc.wantStatus, status, body)
			if tc.wantStatus == http.StatusForbidden {
				assert.JSONEq(t, `{"action":"view","error":"forbidden","resource":"cluster:kf"}`, body)
			} else {
				assert.Contains(t, body, `"host":"broker-1"`)
			}
		})
	}
}

// The topic consumers need topic:view on the topic; the list drops the
// groups the caller may not view.
func TestListTopicConsumers_RBAC(t *testing.T) {
	t.Parallel()

	st := stores{topics: fakeTopics{consumers: []kafkapkg.TopicConsumer{
		{GroupID: "orders-g", PartitionsAssigned: []int32{}},
		{GroupID: "team-g", PartitionsAssigned: []int32{}},
	}}}
	cfg := rbacGrantConfig(perm("topic:orders", "view"), perm("group:team-*", "view"))
	h := rbacFakeServer(t, cfg, st)

	status, body := rbacRequest(t, h, false, "/topics/orders/consumers")
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, []string{"team-g"}, listNames(t, h, rbacListCase{}, "/topics/orders/consumers", "consumers", "group_id"))

	status, body = rbacRequest(t, h, false, "/topics/payments/consumers")
	require.Equal(t, http.StatusForbidden, status, body)
	assert.JSONEq(t, `{"action":"view","error":"forbidden","resource":"topic:payments"}`, body)
}

// RBAC does not apply to private clusters: the middleware lets every
// request through, and no handler filters a list there either. On a
// configured cluster the same policy filters.
func TestRBAC_PrivateClusterListsAreNotFiltered(t *testing.T) {
	t.Parallel()

	st := stores{
		topics: fakeTopics{
			topics: []kafkapkg.TopicInfo{{Name: "orders"}, {Name: "team-a"}},
			consumers: []kafkapkg.TopicConsumer{
				{GroupID: "orders-g", PartitionsAssigned: []int32{}},
				{GroupID: "team-g", PartitionsAssigned: []int32{}},
			},
		},
		groups:  fakeGroups{groups: []kafkapkg.GroupInfo{{GroupID: "orders-g"}, {GroupID: "team-g"}}},
		schemas: fakeSchemas{client: fakeSchemaClient{subjects: []kafkapkg.Subject{{Name: "orders-value", Versions: []int{1}}, {Name: "team-a-value", Versions: []int{1}}}}},
		scram:   fakeSCRAM{users: []kafkapkg.SCRAMUser{{User: "alice", Credentials: []kafkapkg.SCRAMCredential{}}, {User: "team-u", Credentials: []kafkapkg.SCRAMCredential{}}}},
	}
	lists := []struct {
		path, key, field string
		all, team        []string
	}{
		{"/topics", "topics", "name", []string{"orders", "team-a"}, []string{"team-a"}},
		{"/groups", "groups", "group_id", []string{"orders-g", "team-g"}, []string{"team-g"}},
		{"/topics/team-a/consumers", "consumers", "group_id", []string{"orders-g", "team-g"}, []string{"team-g"}},
		{"/schemas/subjects", "subjects", "name", []string{"orders-value", "team-a-value"}, []string{"team-a-value"}},
		{"/users", "users", "user", []string{"alice", "team-u"}, []string{"team-u"}},
	}
	// team sees team-* of every type; none has no grant at all.
	team := rbacGrantConfig(perm("topic:team-*", "view"), perm("group:team-*", "view"),
		perm("schema:team-*", "view"), perm("user:team-*", "view"))
	none := rbacGrantConfig()

	for _, l := range lists {
		t.Run(l.path, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, l.team, listNames(t, rbacFakeServer(t, team, st), rbacListCase{}, l.path, l.key, l.field), "configured cluster")
			assert.Equal(t, l.all, listNames(t, rbacFakeServer(t, team, st), rbacListCase{private: true}, l.path, l.key, l.field), "private cluster, partial grants")
			assert.Equal(t, l.all, listNames(t, rbacFakeServer(t, none, st), rbacListCase{private: true}, l.path, l.key, l.field), "private cluster, no grants")
		})
	}
}
