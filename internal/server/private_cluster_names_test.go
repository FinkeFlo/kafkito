// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// unregisteredAdhocName has the form of an ad-hoc registry name but belongs
// to no registered cluster.
const unregisteredAdhocName = config.AdhocClusterPrefix + "0123456789abcdef"

// privateNamesServer is server.New over a registry with the configured
// cluster "static" and one registered private cluster. It returns the
// private cluster's registry name and the X-Kafkito-Cluster header that
// selects it.
func privateNamesServer(t *testing.T, cfg config.Config, mw ...gen.StrictMiddlewareFunc) (h http.Handler, internal, header string) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "static", Brokers: []string{unreachableBroker}}}, logger)
	t.Cleanup(reg.Close)
	private := config.ClusterConfig{Brokers: []string{unreachableBroker}}
	internal, err := reg.UseAdhoc(private)
	require.NoError(t, err)
	require.True(t, config.IsAdhocClusterName(internal))
	h = New(Options{Version: "test", Logger: logger, Registry: reg, Config: cfg, strictMiddlewares: mw})
	return h, internal, encodeHeader(t, private)
}

// sendCluster sends a request to path with a JSON body and, when set, the
// X-Kafkito-Cluster header, as userMallory.
func sendCluster(h http.Handler, method, path, body, header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(rbacTestHeader, userMallory)
	if header != "" {
		req.Header.Set(PrivateClusterHeader, header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// headerNames returns the sorted names of h.
func headerNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

// A registry name of a private cluster is not a valid {cluster} value on any
// cluster route: with or without the X-Kafkito-Cluster header,
// percent-encoded or not, registered or not and whatever RBAC would decide,
// it is the 404 of an unknown cluster, and neither RBAC nor the handler sees
// it. Only __private__ with the header reaches a handler under that name.
func TestInternalClusterName_RejectedOnEveryClusterRoute(t *testing.T) {
	t.Parallel()

	for _, rc := range []struct {
		name string
		cfg  config.Config
		// allows reports whether the configuration lets every request
		// through, so that __private__ with the header reaches the handler.
		allows bool
	}{
		{"rbac disabled", config.Defaults(), true},
		{"rbac allows all", rbacGrantConfig(perm("*", "*")), true},
		{"rbac allows nothing", rbacGrantConfig(perm("nothing:matches", "view")), false},
	} {
		t.Run(rc.name, func(t *testing.T) {
			t.Parallel()
			var got boundParams
			h, internal, header := privateNamesServer(t, rc.cfg, recordBoundParams(&got))

			for _, op := range apiOps {
				if !strings.Contains(op.pattern, "{cluster}") {
					continue
				}
				for _, tc := range []struct{ name, segment, header, echoed string }{
					{"internal name", internal, "", internal},
					{"internal name and header", internal, header, internal},
					{"escaped internal name", "%5F%5F" + internal[2:], "", internal},
					{"escaped internal name and header", "%5f%5F" + internal[2:], header, internal},
					{"unregistered internal name", unregisteredAdhocName, "", unregisteredAdhocName},
				} {
					got = boundParams{}
					rec := sendCluster(h, op.method, op.path(tc.segment)+validQuery(op.id), validBody(op.id), tc.header)
					require.Equal(t, http.StatusNotFound, rec.Code, "%s %s: %s", op.id, tc.name, rec.Body.String())
					assert.JSONEq(t, `{"error":"unknown cluster: `+tc.echoed+`"}`, rec.Body.String(), "%s %s", op.id, tc.name)
					assert.Zero(t, got.calls, "%s %s: the handler must not run", op.id, tc.name)
				}

				if !rc.allows {
					continue
				}
				got = boundParams{}
				rec := sendCluster(h, op.method, op.path(config.PrivateClusterSentinel)+validQuery(op.id), validBody(op.id), header)
				require.Equal(t, http.StatusTeapot, rec.Code, "%s: %s", op.id, rec.Body.String())
				assert.Equal(t, internal, got.values["Cluster"], op.id)
			}
		})
	}
}

// The 404 for a private cluster's registry name is the one every unknown
// cluster gets: same status, header names and body, naming the cluster as
// the client sent it.
func TestInternalClusterName_SameResponseAsUnknownCluster(t *testing.T) {
	t.Parallel()

	h, internal, header := privateNamesServer(t, config.Defaults())
	for _, route := range []struct{ name, method, path, body string }{
		{"list topics", http.MethodGet, "/topics", ""},
		{"consume", http.MethodGet, "/topics/orders/messages", ""},
		{"produce", http.MethodPost, "/topics/orders/messages", `{"value":"v"}`},
	} {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			unknown := sendCluster(h, route.method, "/api/v1/clusters/nope"+route.path, route.body, "")
			require.Equal(t, http.StatusNotFound, unknown.Code, unknown.Body.String())
			require.JSONEq(t, `{"error":"unknown cluster: nope"}`, unknown.Body.String())

			for _, tc := range []struct{ name, cluster, header string }{
				{"internal name", internal, ""},
				{"internal name and header", internal, header},
				{"unregistered internal name", unregisteredAdhocName, ""},
			} {
				rec := sendCluster(h, route.method, "/api/v1/clusters/"+tc.cluster+route.path, route.body, tc.header)
				assert.Equal(t, unknown.Code, rec.Code, tc.name)
				assert.Equal(t, headerNames(unknown.Header()), headerNames(rec.Header()), tc.name)
				assert.Equal(t, unknown.Header().Get("Content-Type"), rec.Header().Get("Content-Type"), tc.name)
				assert.JSONEq(t, `{"error":"unknown cluster: `+tc.cluster+`"}`, rec.Body.String(), tc.name)
			}
		})
	}
}

// dest_cluster names a configured cluster. A private cluster's registry
// name there is an unknown dest_cluster like any other unknown name, also
// when it is the copy's own source cluster.
func TestCopyMessages_InternalDestClusterIsUnknown(t *testing.T) {
	t.Parallel()

	h, internal, header := privateNamesServer(t, config.Defaults())
	for _, tc := range []struct{ name, source, header, dest, destTopic string }{
		{"unknown name", "static", "", "nope", "orders2"},
		{"internal name", "static", "", internal, "orders2"},
		{"internal name with blanks", "static", "", " " + internal + " ", "orders2"},
		{"unregistered internal name", "static", "", unregisteredAdhocName, "orders2"},
		{"internal name of the source topic", config.PrivateClusterSentinel, header, internal, "orders"},
		{"internal name of the source cluster", config.PrivateClusterSentinel, header, internal, "orders2"},
	} {
		body := `{"dest_cluster":"` + tc.dest + `","dest_topic":"` + tc.destTopic + `"}`
		rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/"+tc.source+"/topics/orders/copy", body, tc.header)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", tc.name, rec.Body.String())
		assert.JSONEq(t, `{"error":"unknown dest_cluster: `+strings.TrimSpace(tc.dest)+`"}`, rec.Body.String(), tc.name)
	}
}
