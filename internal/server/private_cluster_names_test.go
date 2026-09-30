// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

// The 404 for a private cluster's registry name, or for its log name, is
// the one every unknown cluster gets: same status, header names and body,
// naming the cluster as the client sent it.
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
				{"log name", config.ClusterLogName(internal), ""},
				{"log name and header", config.ClusterLogName(internal), header},
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
// name or log name there is an unknown dest_cluster like any other unknown
// name, also when it is the copy's own source cluster.
func TestCopyMessages_InternalDestClusterIsUnknown(t *testing.T) {
	t.Parallel()

	h, internal, header := privateNamesServer(t, config.Defaults())
	for _, tc := range []struct{ name, source, header, dest, destTopic string }{
		{"unknown name", "static", "", "nope", "orders2"},
		{"internal name", "static", "", internal, "orders2"},
		{"internal name with blanks", "static", "", " " + internal + " ", "orders2"},
		{"unregistered internal name", "static", "", unregisteredAdhocName, "orders2"},
		{"log name", "static", "", config.ClusterLogName(internal), "orders2"},
		{"internal name of the source topic", config.PrivateClusterSentinel, header, internal, "orders"},
		{"internal name of the source cluster", config.PrivateClusterSentinel, header, internal, "orders2"},
	} {
		body := `{"dest_cluster":"` + tc.dest + `","dest_topic":"` + tc.destTopic + `"}`
		rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/"+tc.source+"/topics/orders/copy", body, tc.header)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", tc.name, rec.Body.String())
		assert.JSONEq(t, `{"error":"unknown dest_cluster: `+strings.TrimSpace(tc.dest)+`"}`, rec.Body.String(), tc.name)
	}
}

// publicNameClusters is fakeAdhocClusters with a capability probe.
type publicNameClusters struct{ fakeAdhocClusters }

func (publicNameClusters) Capabilities(context.Context, string) (*kafkapkg.Capabilities, error) {
	return &kafkapkg.Capabilities{ListTopics: true}, nil
}

func (publicNameClusters) RefreshCapabilities(string) {}

// publicNameTopics is fakeTopics that also describes any topic.
type publicNameTopics struct{ fakeTopics }

func (publicNameTopics) DescribeTopic(_ context.Context, _, topic string) (*kafkapkg.TopicDetail, error) {
	return &kafkapkg.TopicDetail{Name: topic, Partitions: []kafkapkg.PartitionInfo{}, Configs: []kafkapkg.TopicConfigEntry{}}, nil
}

// publicNameACLs is fakeACLs that lists no ACLs.
type publicNameACLs struct{ fakeACLs }

func (publicNameACLs) ListACLs(context.Context, string) ([]kafkapkg.ACLEntry, error) {
	return []kafkapkg.ACLEntry{}, nil
}

// recordClusterAndServe is a strict middleware that records the bound
// {cluster} value and then runs the handler.
func recordClusterAndServe(got *string) gen.StrictMiddlewareFunc {
	return func(next gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
			*got = reflect.ValueOf(req).FieldByName("Cluster").String()
			return next(ctx, w, r, req)
		}
	}
}

// apiOpByID returns the apiOps entry of operationId id.
func apiOpByID(t *testing.T, id string) apiOp {
	t.Helper()
	for _, op := range apiOps {
		if op.id == id {
			return op
		}
	}
	t.Fatalf("operation %s is not in apiOps", id)
	return apiOp{}
}

// clusterNamingOps returns the operationIds whose 200 JSON response has a
// top-level cluster property.
func clusterNamingOps(t *testing.T) []string {
	t.Helper()
	doc, err := loadSpec()
	require.NoError(t, err)
	var ids []string
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			resp := op.Responses.Status(http.StatusOK)
			if resp == nil || resp.Value == nil {
				continue
			}
			mt := resp.Value.Content.Get("application/json")
			if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
				continue
			}
			if _, ok := mt.Schema.Value.Properties["cluster"]; ok {
				ids = append(ids, op.OperationID)
			}
		}
	}
	slices.Sort(ids)
	return ids
}

// Every response that names its cluster names a private cluster
// __private__, the name the client sent, while the handler works with the
// registry name. A configured cluster keeps its own name.
func TestPrivateClusterResponses_NameThePrivateSentinel(t *testing.T) {
	t.Parallel()

	var handled string
	st := stores{
		configs:  fakeConfigs{},
		clusters: publicNameClusters{},
		topics:   publicNameTopics{fakeTopics{topics: []kafkapkg.TopicInfo{}, consumers: []kafkapkg.TopicConsumer{}}},
		groups:   fakeGroups{groups: []kafkapkg.GroupInfo{}},
		messages: fakeMessages{
			consume: func(kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error) {
				return &kafkapkg.ConsumeResult{Messages: []kafkapkg.Message{}}, nil
			},
			count: func(kafkapkg.CountMessagesOptions) (*kafkapkg.MessageCountResult, error) {
				return &kafkapkg.MessageCountResult{Partitions: []kafkapkg.PartitionMessageCount{}}, nil
			},
			timeline: func(kafkapkg.MessageTimelineOptions) (*kafkapkg.MessageTimelineResult, error) {
				return &kafkapkg.MessageTimelineResult{Slots: []kafkapkg.TimelineSlot{}}, nil
			},
			search: func(kafkapkg.SearchOptions) (*kafkapkg.SearchResult, error) {
				return &kafkapkg.SearchResult{Messages: []kafkapkg.Message{}, Stats: kafkapkg.SearchStats{Direction: kafkapkg.DirNewestFirst}}, nil
			},
		},
		schemas: fakeSchemas{client: fakeSchemaClient{subjects: []kafkapkg.Subject{}}},
		acls:    publicNameACLs{},
		scram:   fakeSCRAM{users: []kafkapkg.SCRAMUser{}},
	}
	h := New(Options{
		Version:           "test",
		Logger:            slog.New(slog.DiscardHandler),
		Config:            config.Defaults(),
		stores:            &st,
		strictMiddlewares: []gen.StrictMiddlewareFunc{recordClusterAndServe(&handled)},
	})
	internal, err := fakeAdhocClusters{}.UseAdhoc(config.ClusterConfig{})
	require.NoError(t, err)
	header := encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
	router := contractRouter(t)

	ops := clusterNamingOps(t)
	require.NotEmpty(t, ops)
	for _, id := range ops {
		op := apiOpByID(t, id)
		for _, tc := range []struct{ name, cluster, header, want string }{
			{"private cluster", config.PrivateClusterSentinel, header, config.PrivateClusterSentinel},
			{"configured cluster", "static", "", "static"},
		} {
			handled = ""
			req := httptest.NewRequest(op.method, op.path(tc.cluster)+validQuery(id), strings.NewReader(validBody(id)))
			req.Header.Set("Content-Type", "application/json")
			if tc.header != "" {
				req.Header.Set(PrivateClusterHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, "%s %s: %s", id, tc.name, rec.Body.String())
			var body struct {
				Cluster *string `json:"cluster"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "%s %s", id, tc.name)
			require.NotNil(t, body.Cluster, "%s %s: no cluster field", id, tc.name)
			assert.Equal(t, tc.want, *body.Cluster, "%s %s", id, tc.name)
			assert.NotContains(t, rec.Body.String(), config.AdhocClusterPrefix, "%s %s", id, tc.name)
			assertResponseMatchesSpec(t, router, req, rec)
			if tc.header != "" {
				assert.Equal(t, internal, handled, "%s: the handler works with the registry name", id)
			}
		}
	}
}

// destCopyRegistry registers every dest_cluster_config under
// unregisteredAdhocName and fails every DescribeTopic with err.
type destCopyRegistry struct {
	copyRegistry
	err error
}

func (destCopyRegistry) ConfigFor(string) (config.ClusterConfig, bool) {
	return config.ClusterConfig{}, false
}

func (destCopyRegistry) UseAdhoc(config.ClusterConfig) (string, error) {
	return unregisteredAdhocName, nil
}

func (f destCopyRegistry) DescribeTopic(context.Context, string, string) (*kafkapkg.TopicDetail, error) {
	return nil, f.err
}

// A copy into a private cluster (dest_cluster_config) that fails the
// destination check names that cluster __private__.
func TestCopyMessages_PrivateDestErrorsNameThePrivateSentinel(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "static", Brokers: []string{unreachableBroker}}}, logger)
	t.Cleanup(reg.Close)
	body := `{"dest_topic":"orders2","dest_cluster_config":{"brokers":["` + unreachableBroker + `"]}}`
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unknown cluster", fmt.Errorf("%w: %s", kafkapkg.ErrUnknownCluster, config.PrivateClusterSentinel), `{"error":"unknown dest_cluster: __private__"}`},
		{"missing topic", kafkapkg.ErrTopicNotFound, `{"error":"dest_topic \"orders2\" does not exist on cluster \"__private__\": create it first (kafkito does not auto-create the destination)"}`},
	} {
		h := New(Options{Version: "test", Logger: logger, Registry: reg, Config: config.Defaults(), copyRegistry: destCopyRegistry{err: tc.err}})
		rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/static/topics/orders/copy", body, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", tc.name, rec.Body.String())
		assert.JSONEq(t, tc.want, rec.Body.String(), tc.name)
	}
}

// privateLogFormats are the handlers of the json and text log formats
// (config.LogConfig) at debug level, with the form a string attribute
// takes in each.
var privateLogFormats = []struct {
	name    string
	handler func(io.Writer) slog.Handler
	attr    func(key, value string) string
}{
	{
		name: "json",
		handler: func(w io.Writer) slog.Handler {
			return slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
		},
		attr: func(key, value string) string { return fmt.Sprintf("%q:%q", key, value) },
	},
	{
		name: "text",
		handler: func(w io.Writer) slog.Handler {
			return slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
		},
		attr: func(key, value string) string { return key + "=" + value },
	},
}

// logLinesWith returns the lines of logs that contain s.
func logLinesWith(logs, s string) []string {
	var lines []string
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, s) {
			lines = append(lines, line)
		}
	}
	return lines
}

// Logs name a private cluster by config.ClusterLogName, never by its
// registry name, in both log formats: the request log, the 5xx error line,
// the Test connection warning, the client warnings and a refused registry
// name in the path.
func TestPrivateClusterLogs_NameTheLogName(t *testing.T) {
	t.Parallel()

	plain := config.ClusterConfig{Brokers: []string{unreachableBroker}}
	insecureTLS := config.ClusterConfig{
		Brokers: []string{unreachableBroker},
		TLS:     config.TLSConfig{Enabled: true, InsecureSkipVerify: true},
	}
	cases := []struct {
		name   string
		cfg    config.ClusterConfig
		method string
		// path's {cluster} is __private__ with the X-Kafkito-Cluster header,
		// or, with registryName, the registry name without the header.
		path         string
		registryName bool
		wantCode     int
		// wantMsg is a message whose log lines all name the cluster.
		wantMsg string
	}{
		{
			name: "list topics", cfg: plain, method: http.MethodGet, path: "/api/v1/clusters/{cluster}/topics",
			wantCode: http.StatusBadGateway, wantMsg: "upstream kafka error",
		},
		{
			name: "list topics request log", cfg: plain, method: http.MethodGet, path: "/api/v1/clusters/{cluster}/topics",
			wantCode: http.StatusBadGateway, wantMsg: "http request",
		},
		{
			name: "consume", cfg: plain, method: http.MethodGet, path: "/api/v1/clusters/{cluster}/topics/orders/messages",
			wantCode: http.StatusBadGateway, wantMsg: "upstream kafka error",
		},
		{
			name: "schema registry not configured", cfg: plain, method: http.MethodGet,
			path: "/api/v1/clusters/{cluster}/schemas/subjects", wantCode: http.StatusNotFound, wantMsg: "http request",
		},
		{
			name: "insecure TLS", cfg: insecureTLS, method: http.MethodGet, path: "/api/v1/clusters/{cluster}/topics",
			wantCode: http.StatusBadGateway, wantMsg: "TLS verification disabled for cluster",
		},
		{
			name: "test connection", cfg: plain, method: http.MethodPost, path: "/api/v1/clusters/_test",
			wantCode: http.StatusOK, wantMsg: "testCluster ping failed",
		},
		{
			name: "registry name in the path", cfg: plain, method: http.MethodGet, path: "/api/v1/clusters/{cluster}/topics",
			registryName: true, wantCode: http.StatusNotFound, wantMsg: "http request",
		},
	}
	for _, format := range privateLogFormats {
		for _, tc := range cases {
			t.Run(format.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				logs := &syncBuffer{}
				logger := slog.New(format.handler(logs))
				reg := kafkapkg.NewRegistry(nil, logger)
				internal, err := reg.UseAdhoc(tc.cfg)
				require.NoError(t, err)
				cfg := config.Defaults()
				cfg.Server.TestConnectionTimeout = 300 * time.Millisecond
				h := New(Options{Version: "test", Logger: logger, Registry: reg, Config: cfg})

				segment, header := config.PrivateClusterSentinel, encodeHeader(t, tc.cfg)
				if tc.registryName {
					segment, header = internal, ""
				}
				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer cancel()
				req := httptest.NewRequestWithContext(ctx, tc.method, strings.Replace(tc.path, "{cluster}", segment, 1), nil)
				if header != "" {
					req.Header.Set(PrivateClusterHeader, header)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				reg.Close()

				require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
				logged := logs.String()
				assert.NotContains(t, logged, config.AdhocClusterPrefix, "the registry name is never logged")
				lines := logLinesWith(logged, tc.wantMsg)
				require.NotEmpty(t, lines, "no %q line in:\n%s", tc.wantMsg, logged)
				for _, line := range lines {
					assert.Contains(t, line, format.attr("cluster", config.ClusterLogName(internal)))
				}
			})
		}
	}
}

// preflightFailingCopyRegistry is a failingCopyRegistry whose DescribeTopic
// fails for private clusters.
type preflightFailingCopyRegistry struct{ *failingCopyRegistry }

func (f preflightFailingCopyRegistry) DescribeTopic(ctx context.Context, cluster, topic string) (*kafkapkg.TopicDetail, error) {
	if config.IsAdhocClusterName(cluster) {
		return nil, errors.New("describe failed")
	}
	return f.failingCopyRegistry.DescribeTopic(ctx, cluster, topic)
}

// A copy job's warnings name a private source or destination by its log
// name. Sequential: the job holds a copy slot.
func TestCopyStream_WarningsNameTheLogName(t *testing.T) {
	private := config.ClusterConfig{Brokers: []string{unreachableBroker}}
	const (
		privateSource = "/api/v1/clusters/__private__/topics/orders/copy"
		privateDest   = `{"dest_cluster_config":{"brokers":["` + unreachableBroker + `"]},"dest_topic":"orders2","limit":1}`
	)
	cases := []struct {
		name, path, body string
		header           bool
		consume, produce error
		describeFails    bool
		wantMsg          string
	}{
		{
			name: "blocked private source", path: privateSource, header: true,
			body: `{"dest_cluster":"dst","dest_topic":"orders2"}`, consume: blockedDialErr(),
			wantMsg: "copy: blocked address",
		},
		{
			name: "blocked private destination", path: "/api/v1/clusters/src/topics/orders/copy",
			body: privateDest, produce: blockedDialErr(), wantMsg: "copy: blocked address",
		},
		{
			name: "private destination pre-flight", path: "/api/v1/clusters/src/topics/orders/copy",
			body: privateDest, describeFails: true, wantMsg: "copy: destination pre-flight check skipped",
		},
		{
			name: "private source preserve_partition pre-flight", path: privateSource, header: true,
			body:          `{"dest_cluster":"dst","dest_topic":"orders2","preserve_partition":true,"limit":1}`,
			describeFails: true, wantMsg: "copy: preserve_partition pre-flight check skipped",
		},
	}
	for _, format := range privateLogFormats {
		for _, tc := range cases {
			t.Run(format.name+"/"+tc.name, func(t *testing.T) {
				logs := &syncBuffer{}
				logger := slog.New(format.handler(logs))
				reg := kafkapkg.NewRegistry([]config.ClusterConfig{
					{Name: "src", Brokers: []string{"127.0.0.1:19092"}},
					{Name: "dst", Brokers: []string{"127.0.0.1:19093"}},
				}, logger)
				t.Cleanup(reg.Close)
				internal, err := reg.UseAdhoc(private)
				require.NoError(t, err)
				base := newBlockingCopyRegistry(reg)
				base.unblockNow()
				failing := &failingCopyRegistry{blockingCopyRegistry: base, consumeErr: tc.consume, produceErr: tc.produce}
				var fake copyRegistry = failing
				if tc.describeFails {
					fake = preflightFailingCopyRegistry{failing}
				}
				h := New(Options{Version: "test", Logger: logger, Registry: reg, Config: config.Defaults(), copyRegistry: fake})

				req := newCopyRequest(tc.path, tc.body)
				if tc.header {
					req.Header.Set(PrivateClusterHeader, encodeHeader(t, private))
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond,
					"the copy job releases its slot")

				logged := logs.String()
				assert.NotContains(t, logged, config.AdhocClusterPrefix, "the registry name is never logged")
				lines := logLinesWith(logged, tc.wantMsg)
				require.Len(t, lines, 1, logged)
				assert.Contains(t, lines[0], format.attr("cluster", config.ClusterLogName(internal)))
			})
		}
	}
}

// Test connection's warnings name the probed private cluster by its log
// name.
func TestTestCluster_WarningsNameTheLogName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		fc      fakeClusters
		wantMsg string
	}{
		{"ping failed", fakeClusters{ping: errors.New("dial timeout")}, "testCluster ping failed"},
		{"broker probe failed", fakeClusters{issues: []kafkapkg.BrokerIssue{blockedLocalhostIssue}}, "testCluster broker probe failed"},
	}
	for _, format := range privateLogFormats {
		for _, tc := range cases {
			t.Run(format.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				logs := &syncBuffer{}
				h := New(Options{
					Version: "test", Logger: slog.New(format.handler(logs)), Config: config.Defaults(),
					stores: &stores{clusters: tc.fc},
				})
				rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", testClusterBody, "")
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				internal, err := tc.fc.UseAdhoc(config.ClusterConfig{})
				require.NoError(t, err)

				logged := logs.String()
				assert.NotContains(t, logged, config.AdhocClusterPrefix, "the registry name is never logged")
				lines := logLinesWith(logged, tc.wantMsg)
				require.Len(t, lines, 1, logged)
				assert.Contains(t, lines[0], format.attr("cluster", config.ClusterLogName(internal)))
			})
		}
	}
}
