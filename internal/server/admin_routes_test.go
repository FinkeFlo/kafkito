// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// fakeSchemaRegistry is an in-memory Confluent-compatible Schema Registry
// with just the endpoints kafkito calls. It records every request.
type fakeSchemaRegistry struct {
	*httptest.Server

	mu       sync.Mutex
	subjects map[string][]kafkapkg.SchemaVersion
	nextID   int
	requests []string // "METHOD escaped-path?query"
	bodies   []string
}

func startFakeSchemaRegistry(t *testing.T) *fakeSchemaRegistry {
	t.Helper()
	sr := &fakeSchemaRegistry{subjects: map[string][]kafkapkg.SchemaVersion{}, nextID: 1}
	sr.Server = httptest.NewServer(http.HandlerFunc(sr.serve))
	t.Cleanup(sr.Close)
	return sr
}

// register adds a version to subject and returns its id.
func (sr *fakeSchemaRegistry) register(subject, schemaType, schema string) int {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	id := sr.nextID
	sr.nextID++
	vs := sr.subjects[subject]
	sr.subjects[subject] = append(vs, kafkapkg.SchemaVersion{Subject: subject, ID: id, Version: len(vs) + 1, SchemaType: schemaType, Schema: schema})
	return id
}

func (sr *fakeSchemaRegistry) recorded() []string {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return append([]string(nil), sr.requests...)
}

func (sr *fakeSchemaRegistry) lastBody() string {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if len(sr.bodies) == 0 {
		return ""
	}
	return sr.bodies[len(sr.bodies)-1]
}

func srError(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error_code": code, "message": msg})
}

func srJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	_ = json.NewEncoder(w).Encode(v)
}

func (sr *fakeSchemaRegistry) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	sr.mu.Lock()
	rec := r.Method + " " + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		rec += "?" + r.URL.RawQuery
	}
	sr.requests = append(sr.requests, rec)
	if len(body) > 0 {
		sr.bodies = append(sr.bodies, string(body))
	}
	sr.mu.Unlock()

	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	unescape := func(s string) string { u, _ := url.PathUnescape(s); return u }
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "subjects":
		sr.mu.Lock()
		names := make([]string, 0, len(sr.subjects))
		for n := range sr.subjects {
			names = append(names, n)
		}
		sr.mu.Unlock()
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		srJSON(w, names)
	case parts[0] == "config":
		srJSON(w, map[string]string{"compatibilityLevel": "BACKWARD"})
	case parts[0] == "subjects" && len(parts) >= 2:
		subject := unescape(parts[1])
		sr.mu.Lock()
		vs, ok := sr.subjects[subject]
		sr.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "versions":
			var req kafkapkg.RegisterSchemaRequest
			if err := json.Unmarshal(body, &req); err != nil || req.Schema == "" {
				srError(w, http.StatusUnprocessableEntity, 42201, "Invalid schema")
				return
			}
			srJSON(w, map[string]int{"id": sr.register(subject, req.SchemaType, req.Schema)})
		case !ok:
			srError(w, http.StatusNotFound, 40401, "Subject '"+subject+"' not found.")
		case r.Method == http.MethodDelete && len(parts) == 2:
			out := make([]int, 0, len(vs))
			for _, v := range vs {
				out = append(out, v.Version)
			}
			if r.URL.Query().Get("permanent") == "true" {
				sr.mu.Lock()
				delete(sr.subjects, subject)
				sr.mu.Unlock()
			}
			srJSON(w, out)
		case r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "versions":
			out := make([]int, 0, len(vs))
			for _, v := range vs {
				out = append(out, v.Version)
			}
			srJSON(w, out)
		case r.Method == http.MethodGet && len(parts) == 4 && parts[2] == "versions":
			if parts[3] == "latest" {
				srJSON(w, vs[len(vs)-1])
				return
			}
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				srError(w, http.StatusUnprocessableEntity, 42202, "The specified version '"+unescape(parts[3])+"' is not a valid version id.")
				return
			}
			if n < 1 || n > len(vs) {
				srError(w, http.StatusNotFound, 40402, "Version "+parts[3]+" not found.")
				return
			}
			srJSON(w, vs[n-1])
		default:
			srError(w, http.StatusNotFound, 404, "not found")
		}
	default:
		srError(w, http.StatusNotFound, 404, "not found")
	}
}

// fetchOffsetsLikeKafka makes c answer an OffsetFetch for a group without
// commits as Kafka does, with no committed offsets instead of
// GROUP_ID_NOT_FOUND, so a consumer group can be created by committing its
// first offsets.
func fetchOffsetsLikeKafka(c *kfake.Cluster) {
	var mu sync.Mutex
	known := map[string]bool{}
	c.ControlKey(int16(kmsg.OffsetCommit), func(req kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()
		mu.Lock()
		known[req.(*kmsg.OffsetCommitRequest).Group] = true
		mu.Unlock()
		return nil, nil, false
	})
	c.ControlKey(int16(kmsg.DeleteGroups), func(req kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()
		mu.Lock()
		for _, g := range req.(*kmsg.DeleteGroupsRequest).Groups {
			delete(known, g)
		}
		mu.Unlock()
		return nil, nil, false
	})
	c.ControlKey(int16(kmsg.OffsetFetch), func(req kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()
		r := req.(*kmsg.OffsetFetchRequest)
		group := r.Group
		if r.Version >= 8 {
			if len(r.Groups) != 1 {
				return nil, nil, false
			}
			group = r.Groups[0].Group
		}
		mu.Lock()
		defer mu.Unlock()
		if known[group] {
			return nil, nil, false
		}
		resp := r.ResponseKind().(*kmsg.OffsetFetchResponse)
		if r.Version >= 8 {
			g := kmsg.NewOffsetFetchResponseGroup()
			g.Group = group
			resp.Groups = append(resp.Groups, g)
		}
		return resp, nil, true
	})
}

// timeoutShort bounds requests against the unreachable cluster "down".
const timeoutShort = time.Second

// adminServer serves server.New against an in-memory Kafka cluster with the
// topic "orders", registered as "kf" (with the fake Schema Registry), as the
// production cluster "kfprod" and as "nosr" (without a Schema Registry).
func adminServer(t *testing.T, sr *fakeSchemaRegistry) http.Handler {
	t.Helper()
	c := newKfake(t, "orders")
	fetchOffsetsLikeKafka(c)
	addr := c.ListenAddrs()[0]
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{
		{Name: "kf", Brokers: []string{addr}, SchemaRegistry: config.SchemaRegistryConfig{URL: sr.URL}},
		{Name: "kfprod", Brokers: []string{addr}, IsProd: true, SchemaRegistry: config.SchemaRegistryConfig{URL: sr.URL}},
		{Name: "nosr", Brokers: []string{addr}},
		{Name: "down", Brokers: []string{"127.0.0.1:1"}, SchemaRegistry: config.SchemaRegistryConfig{URL: "http://127.0.0.1:1"}},
	}, slog.Default())
	t.Cleanup(reg.Close)
	return New(Options{Version: "v-test", Logger: slog.Default(), Registry: reg, Config: config.Defaults()})
}

// adminCases walks consumer groups, schemas, ACLs and SCRAM users on an
// in-memory cluster and a fake Schema Registry: the cases run in order and
// depend on each other's side effects.
func adminCases(t *testing.T) []handlerCase {
	t.Helper()
	sr := startFakeSchemaRegistry(t)
	h := adminServer(t, sr)
	const (
		jsonCT   = "application/json"
		confirm  = "X-Kafkito-Confirm-Prod"
		groups   = "/api/v1/clusters/kf/groups"
		group    = groups + "/g1"
		prodGrp  = "/api/v1/clusters/kfprod/groups/g1"
		subjects = "/api/v1/clusters/kf/schemas/subjects"
		acls     = "/api/v1/clusters/kf/acls"
		users    = "/api/v1/clusters/kf/users"
	)
	acl := func(fields string) string {
		return `{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"` + fields + `}`
	}
	aclWithout := func(field string) string {
		var m map[string]string
		require.NoError(t, json.Unmarshal([]byte(acl("")), &m))
		delete(m, field)
		b, err := json.Marshal(m)
		require.NoError(t, err)
		return string(b)
	}
	cases := []requestCase{
		// listGroups
		{name: "groups empty", method: "GET", path: groups, wantStatus: 200, wantBody: `{"cluster":"kf","groups":[]}`},
		{name: "groups unknown cluster", method: "GET", path: "/api/v1/clusters/nope/groups", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "groups unreachable", method: "GET", path: "/api/v1/clusters/down/groups", timeout: timeoutShort, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "groups wrong method", method: "PUT", path: groups, wantStatus: 405, notAnOperation: true},
		// createGroup
		{name: "create group dry run", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g1","topic":"orders","strategy":"earliest","dry_run":true}`, wantStatus: 200, wantBody: `"dry_run":true`},
		{name: "create group", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g1","topic":"orders","strategy":"latest"}`, wantStatus: 200, wantBody: `{"group":"g1","topic":"orders","dry_run":false,"results":[{"partition":0,"old_offset":-1,"new_offset":0,"end_offset":0}]}`},
		{name: "create group exists", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g1","topic":"orders","strategy":"latest"}`, wantStatus: 409, wantBody: `{"error":"kafka: consumer group already exists"}`},
		{name: "create group with offset", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g-offset","topic":"orders","strategy":"offset","offset":0}`, wantStatus: 200, wantBody: `"group":"g-offset"`},
		{name: "create group shift-by", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g2","topic":"orders","strategy":"shift-by"}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/strategy\": must be one of the allowed values"}`},
		{name: "create group unknown strategy", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g2","topic":"orders","strategy":"newest"}`, wantStatus: 400, wantBody: `"error":"request body \"/strategy\": must be one of the allowed values"`},
		{name: "create group without group_id", method: "POST", path: groups, contentType: jsonCT, body: `{"topic":"orders","strategy":"latest"}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/group_id\": is required"}`},
		{name: "create group blank group_id", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":" ","topic":"orders","strategy":"latest"}`, wantStatus: 400, wantBody: `{"error":"kafka: create group: group_id required"}`},
		{name: "create group blank topic", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g2","topic":"","strategy":"latest"}`, wantStatus: 400, wantBody: `{"error":"kafka: create group: topic required"}`},
		{name: "create group timestamp without timestamp_ms", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g2","topic":"orders","strategy":"timestamp"}`, wantStatus: 400, wantBody: `{"error":"kafka: create group: timestamp_ms required for timestamp strategy"}`},
		{name: "create group unknown topic", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g2","topic":"missing","strategy":"latest"}`, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "create group unknown field", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g2","topic":"orders","strategy":"latest","shift":1}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: has properties that are not allowed"}`},
		{name: "create group offset not an integer", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":"g2","topic":"orders","strategy":"offset","offset":"1"}`, wantStatus: 400, wantBody: `"error":"request body \"/offset\": must be of type integer"`},
		{name: "create group malformed", method: "POST", path: groups, contentType: jsonCT, body: `{"group_id":`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: malformed"}`},
		{name: "create group without body", method: "POST", path: groups, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "create group unknown cluster", method: "POST", path: "/api/v1/clusters/nope/groups", contentType: jsonCT, body: `{"group_id":"g2","topic":"orders","strategy":"latest"}`, wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		// The former handler echoed the broker error in the 502 body.
		{name: "create group unreachable", method: "POST", path: "/api/v1/clusters/down/groups", contentType: jsonCT, body: `{"group_id":"g2","topic":"orders","strategy":"latest"}`, timeout: timeoutShort, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "groups", method: "GET", path: groups, wantStatus: 200, wantBody: `"group_id":"g1"`},
		// describeGroup
		{name: "describe group", method: "GET", path: group, wantStatus: 200, wantBody: `"offsets":[{"topic":"orders","partition":0,"offset":0,"log_end":0,"lag":0}]`},
		{name: "describe group unknown cluster", method: "GET", path: "/api/v1/clusters/nope/groups/g1", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "describe group unreachable", method: "GET", path: "/api/v1/clusters/down/groups/g1", timeout: timeoutShort, wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		// resetGroupOffsets
		{name: "reset dry run", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"earliest","dry_run":true}`, wantStatus: 200, wantBody: `"dry_run":true`},
		{name: "reset shift-by", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","partitions":[0],"strategy":"shift-by","shift":-1}`, wantStatus: 200, wantBody: `{"group":"g1","topic":"orders","dry_run":false,"results":[{"partition":0,"old_offset":0,"new_offset":0,"end_offset":0}]}`},
		{name: "reset to offset", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"offset","offset":0}`, wantStatus: 200},
		{name: "reset timestamp", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"timestamp","timestamp_ms":1}`, wantStatus: 200},
		{name: "reset timestamp without timestamp_ms", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"timestamp"}`, wantStatus: 400, wantBody: `{"error":"kafka: reset offsets: timestamp_ms required for timestamp strategy"}`},
		{name: "reset unknown strategy", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"newest"}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/strategy\": must be one of the allowed values"}`},
		{name: "reset without strategy", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders"}`, wantStatus: 400, wantBody: `"error":"request body \"/strategy\": is required"`},
		{name: "reset without topic", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"strategy":"latest"}`, wantStatus: 400, wantBody: `"error":"request body \"/topic\": is required"`},
		{name: "reset blank topic", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":" ","strategy":"latest"}`, wantStatus: 400, wantBody: `{"error":"kafka: reset offsets: topic required"}`},
		{name: "reset unknown topic", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"missing","strategy":"latest"}`, wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		{name: "reset partition int32 overflow", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","partitions":[2147483648],"strategy":"latest"}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "reset partitions not an array", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","partitions":0,"strategy":"latest"}`, wantStatus: 400, wantBody: `"error":"request body \"/partitions\": must be of type array"`},
		{name: "reset unknown field", method: "POST", path: group + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"latest","group_id":"x"}`, wantStatus: 400, wantBody: `"error":"request body: has properties that are not allowed"`},
		{name: "reset unknown cluster", method: "POST", path: "/api/v1/clusters/nope/groups/g1/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"latest"}`, wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "reset prod without confirmation", method: "POST", path: prodGrp + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"latest"}`, wantStatus: 428, wantBody: `{"code":"production_confirmation_required","error":"production cluster: resend with X-Kafkito-Confirm-Prod: true after user confirmation"}`},
		// The request is validated before the production check, as on the
		// other operations with a production confirmation.
		{name: "reset prod without confirmation, invalid body", method: "POST", path: prodGrp + "/reset-offsets", contentType: jsonCT, body: `{"topic":"orders","strategy":"newest"}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "reset prod confirmation not exactly true", method: "POST", path: prodGrp + "/reset-offsets", contentType: jsonCT, header: map[string]string{confirm: "TRUE"}, body: `{"topic":"orders","strategy":"latest"}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "reset prod confirmed", method: "POST", path: prodGrp + "/reset-offsets", contentType: jsonCT, header: map[string]string{confirm: "true"}, body: `{"topic":"orders","strategy":"latest"}`, wantStatus: 200, wantBody: `"group":"g1"`},
		// deleteGroup: not guarded by the production confirmation.
		{name: "delete prod group without confirmation", method: "DELETE", path: "/api/v1/clusters/kfprod/groups/g-offset", wantStatus: 200, wantBody: `{"deleted":"g-offset"}`},
		{name: "delete group", method: "DELETE", path: group, wantStatus: 200, wantBody: `{"deleted":"g1"}`},
		{name: "delete group again", method: "DELETE", path: group, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "delete group unknown cluster", method: "DELETE", path: "/api/v1/clusters/nope/groups/g1", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "groups after delete", method: "GET", path: groups, wantStatus: 200, wantBody: `{"cluster":"kf","groups":[]}`},

		// registerSchema
		{name: "register schema", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schema":"{\"type\":\"string\"}"}`, wantStatus: 200, wantBody: `{"id":1}`},
		{name: "register schema json type", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schema":"{}","schemaType":"JSON","references":[{"name":"r","subject":"s","version":1}]}`, wantStatus: 200, wantBody: `{"id":2}`},
		{name: "register schema lower-case type", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schema":"{}","schemaType":"json"}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/schemaType\": must be one of the allowed values"}`},
		{name: "register schema without schema", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schemaType":"AVRO"}`, wantStatus: 400, wantBody: `"error":"request body \"/schema\": is required"`},
		{name: "register schema unknown field", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schema":"{}","id":1}`, wantStatus: 400, wantBody: `"error":"request body: has properties that are not allowed"`},
		{name: "register schema reference without version", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schema":"{}","references":[{"name":"r","subject":"s"}]}`, wantStatus: 400, wantBody: `"error":"request body \"/references/0/version\": is required"`},
		{name: "register schema malformed", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schema":`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: malformed"}`},
		{name: "register schema rejected by registry", method: "POST", path: subjects + "/orders-value/versions", contentType: jsonCT, body: `{"schema":""}`, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "register schema without registry", method: "POST", path: "/api/v1/clusters/nosr/schemas/subjects/orders-value/versions", contentType: jsonCT, body: `{"schema":"{}"}`, wantStatus: 404, wantBody: `{"error":"schema registry not configured for cluster: nosr"}`},
		{name: "register schema unknown cluster", method: "POST", path: "/api/v1/clusters/nope/schemas/subjects/orders-value/versions", contentType: jsonCT, body: `{"schema":"{}"}`, wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "register schema subject with slash", method: "POST", path: subjects + "/a%2Fb/versions", contentType: jsonCT, body: `{"schema":"\"int\""}`, wantStatus: 200, wantBody: `{"id":3}`},
		// listSubjects
		{name: "subjects", method: "GET", path: subjects, wantStatus: 200, wantBody: `{"cluster":"kf","subjects":[{"name":"a/b","versions":[1]},{"name":"orders-value","versions":[1,2],"latest_schema_type":"JSON"}]}`},
		{name: "subjects without registry", method: "GET", path: "/api/v1/clusters/nosr/schemas/subjects", wantStatus: 404, wantBody: `{"error":"schema registry not configured for cluster: nosr"}`},
		{name: "subjects unknown cluster", method: "GET", path: "/api/v1/clusters/nope/schemas/subjects", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "subjects unreachable registry", method: "GET", path: "/api/v1/clusters/down/schemas/subjects", wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		// listSchemaVersions
		{name: "versions", method: "GET", path: subjects + "/orders-value/versions", wantStatus: 200, wantBody: `{"subject":"orders-value","versions":[1,2]}`},
		{name: "versions subject with slash", method: "GET", path: subjects + "/a%2Fb/versions", wantStatus: 200, wantBody: `{"subject":"a/b","versions":[1]}`},
		{name: "versions unknown subject", method: "GET", path: subjects + "/missing/versions", wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "versions without registry", method: "GET", path: "/api/v1/clusters/nosr/schemas/subjects/orders-value/versions", wantStatus: 404},
		// getSchemaVersion
		{name: "version latest", method: "GET", path: subjects + "/orders-value/versions/latest", wantStatus: 200, wantBody: `{"subject":"orders-value","id":2,"version":2,"schemaType":"JSON","schema":"{}","config":{"compatibilityLevel":"BACKWARD"}}`},
		{name: "version number", method: "GET", path: subjects + "/orders-value/versions/1", wantStatus: 200, wantBody: `"schema":"{\"type\":\"string\"}"`},
		{name: "version -1", method: "GET", path: subjects + "/orders-value/versions/-1", wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		// The registry validates the version, as before.
		{name: "version not a number", method: "GET", path: subjects + "/orders-value/versions/first", wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "version unknown", method: "GET", path: subjects + "/orders-value/versions/9", wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		{name: "version without registry", method: "GET", path: "/api/v1/clusters/nosr/schemas/subjects/orders-value/versions/latest", wantStatus: 404, wantBody: `{"error":"schema registry not configured for cluster: nosr"}`},
		// deleteSubject
		{name: "delete subject soft", method: "DELETE", path: subjects + "/orders-value", wantStatus: 200, wantBody: `{"deleted":"orders-value","permanent":false,"versions":[1,2]}`},
		{name: "delete subject permanent=false", method: "DELETE", path: subjects + "/orders-value?permanent=false", wantStatus: 200, wantBody: `"permanent":false`},
		{name: "delete subject permanent not a boolean", method: "DELETE", path: subjects + "/orders-value?permanent=yes", wantStatus: 400, wantBody: `{"code":"invalid_request","error":"parameter \"permanent\" in query: invalid format"}`},
		{name: "delete subject permanent empty", method: "DELETE", path: subjects + "/orders-value?permanent=", wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "delete subject permanent=1 rejected", method: "DELETE", path: subjects + "/orders-value?permanent=1", wantStatus: 400, wantBody: `{"code":"invalid_request","error":"parameter \"permanent\" in query: must be true or false"}`},
		{name: "delete subject permanent=TRUE rejected", method: "DELETE", path: subjects + "/orders-value?permanent=TRUE", wantStatus: 400, wantBody: `{"code":"invalid_request","error":"parameter \"permanent\" in query: must be true or false"}`},
		{name: "delete subject permanent=t rejected", method: "DELETE", path: subjects + "/orders-value?permanent=t", wantStatus: 400, wantBody: `{"code":"invalid_request","error":"parameter \"permanent\" in query: must be true or false"}`},
		{name: "delete subject permanent=0 rejected", method: "DELETE", path: subjects + "/orders-value?permanent=0", wantStatus: 400, wantBody: `{"code":"invalid_request","error":"parameter \"permanent\" in query: must be true or false"}`},
		{name: "delete subject permanent repeated", method: "DELETE", path: subjects + "/orders-value?permanent=true&permanent=1", wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "delete subject permanent", method: "DELETE", path: subjects + "/orders-value?permanent=true", wantStatus: 200, wantBody: `{"deleted":"orders-value","permanent":true,"versions":[1,2]}`},
		{name: "delete subject unknown", method: "DELETE", path: subjects + "/orders-value", wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "delete subject without registry", method: "DELETE", path: "/api/v1/clusters/nosr/schemas/subjects/orders-value", wantStatus: 404},
		// Not guarded by the production confirmation.
		{name: "delete prod subject without confirmation", method: "DELETE", path: "/api/v1/clusters/kfprod/schemas/subjects/a%2Fb", wantStatus: 200, wantBody: `{"deleted":"a/b","permanent":false,"versions":[1]}`},

		// listAcls
		{name: "acls empty", method: "GET", path: acls, wantStatus: 200, wantBody: `{"acls":[],"cluster":"kf"}`},
		{name: "acls unknown cluster", method: "GET", path: "/api/v1/clusters/nope/acls", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "acls unreachable", method: "GET", path: "/api/v1/clusters/down/acls", timeout: timeoutShort, wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		// createAcl
		{name: "create acl", method: "POST", path: acls, contentType: jsonCT, body: acl(""), wantStatus: 201, wantBody: `{"acl":{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"},"ok":true}`},
		// Enum-like fields stay case-insensitive and unknown fields are
		// ignored, as before.
		{name: "create acl lower case, extra field", method: "POST", path: acls, contentType: jsonCT, body: `{"principal":"User:bob","host":"*","resource_type":"group","resource_name":"g","pattern_type":"prefixed","operation":"read","permission_type":"deny","note":"x"}`, wantStatus: 201, wantBody: `"resource_type":"group"`},
		{name: "create acl empty host", method: "POST", path: acls, contentType: jsonCT, body: `{"principal":"User:carol","host":"","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"WRITE","permission_type":"ALLOW"}`, wantStatus: 201, wantBody: `"host":""`},
		{name: "create acl without host", method: "POST", path: acls, contentType: jsonCT, body: aclWithout("host"), wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/host\": is required"}`},
		{name: "create acl without principal", method: "POST", path: acls, contentType: jsonCT, body: aclWithout("principal"), wantStatus: 400, wantBody: `"error":"request body \"/principal\": is required"`},
		{name: "create acl blank principal", method: "POST", path: acls, contentType: jsonCT, body: `{"principal":" ","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`, wantStatus: 400, wantBody: `{"error":"kafka: principal is required"}`},
		{name: "create acl unknown operation", method: "POST", path: acls, contentType: jsonCT, body: `{"principal":"User:a","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"FLY","permission_type":"ALLOW"}`, wantStatus: 400, wantBody: `"error":"kafka: operation: `},
		{name: "create acl ANY permission", method: "POST", path: acls, contentType: jsonCT, body: `{"principal":"User:a","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ANY"}`, wantStatus: 400, wantBody: `"error":"kafka: permission_type`},
		{name: "create acl wrong type", method: "POST", path: acls, contentType: jsonCT, body: `{"principal":1,"host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`, wantStatus: 400, wantBody: `"error":"request body \"/principal\": must be of type string"`},
		{name: "create acl malformed", method: "POST", path: acls, contentType: jsonCT, body: `{"principal":`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: malformed"}`},
		{name: "create acl without body", method: "POST", path: acls, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "create acl unknown cluster", method: "POST", path: "/api/v1/clusters/nope/acls", contentType: jsonCT, body: acl(""), wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "create acl unreachable", method: "POST", path: "/api/v1/clusters/down/acls", contentType: jsonCT, body: acl(""), timeout: timeoutShort, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "acls", method: "GET", path: acls, wantStatus: 200, wantBody: `{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`},
		// deleteAcl: DELETE with a JSON body.
		{name: "delete acl", method: "DELETE", path: acls, contentType: jsonCT, body: acl(""), wantStatus: 200, wantBody: `{"deleted":1,"ok":true}`},
		{name: "delete acl no match", method: "DELETE", path: acls, contentType: jsonCT, body: acl(""), wantStatus: 200, wantBody: `{"deleted":0,"ok":true}`},
		{name: "delete acl filter ANY", method: "DELETE", path: acls, contentType: jsonCT, body: `{"principal":"User:bob","host":"*","resource_type":"ANY","resource_name":"g","pattern_type":"ANY","operation":"ANY","permission_type":"ANY"}`, wantStatus: 400, wantBody: `"error":"kafka: resource_type`},
		{name: "delete acl lower case", method: "DELETE", path: acls, contentType: jsonCT, body: `{"principal":"User:bob","host":"*","resource_type":"group","resource_name":"g","pattern_type":"prefixed","operation":"read","permission_type":"deny"}`, wantStatus: 200, wantBody: `{"deleted":1,"ok":true}`},
		{name: "delete acl without resource_name", method: "DELETE", path: acls, contentType: jsonCT, body: aclWithout("resource_name"), wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/resource_name\": is required"}`},
		{name: "delete acl blank resource_name", method: "DELETE", path: acls, contentType: jsonCT, body: `{"principal":"User:a","host":"*","resource_type":"TOPIC","resource_name":"","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`, wantStatus: 400, wantBody: `{"error":"kafka: resource_name is required"}`},
		{name: "delete acl without body", method: "DELETE", path: acls, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "delete acl wrong content type", method: "DELETE", path: acls, contentType: "text/plain", body: acl(""), wantStatus: 400, wantBody: `"error":"request body: unsupported Content-Type"`},
		{name: "delete acl unknown cluster", method: "DELETE", path: "/api/v1/clusters/nope/acls", contentType: jsonCT, body: acl(""), wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},

		// listScramUsers
		{name: "users empty", method: "GET", path: users, wantStatus: 200, wantBody: `{"cluster":"kf","users":[]}`},
		{name: "users unknown cluster", method: "GET", path: "/api/v1/clusters/nope/users", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "users unreachable", method: "GET", path: "/api/v1/clusters/down/users", timeout: timeoutShort, wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		// upsertScramUser
		{name: "upsert user", method: "POST", path: users, contentType: jsonCT, body: `{"user":"alice","mechanism":"SCRAM-SHA-256","password":"pw-alice"}`, wantStatus: 200, wantBody: `{"mechanism":"SCRAM-SHA-256","ok":true,"user":"alice"}`},
		{name: "upsert user 512 min iterations", method: "POST", path: users, contentType: jsonCT, body: `{"user":"alice","mechanism":"SCRAM-SHA-512","password":"pw-alice","iterations":4096}`, wantStatus: 200},
		{name: "upsert user max iterations, extra field", method: "POST", path: users, contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256","password":"pw-bob","iterations":16384,"note":"x"}`, wantStatus: 200, wantBody: `"user":"bob"`},
		{name: "upsert user iterations below range", method: "POST", path: users, contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256","password":"pw-bob","iterations":4095}`, wantStatus: 400, wantBody: `{"error":"kafka: iterations 4095 out of range [4096,16384]"}`},
		{name: "upsert user iterations above range", method: "POST", path: users, contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256","password":"pw-bob","iterations":16385}`, wantStatus: 400, wantBody: `{"error":"kafka: iterations 16385 out of range [4096,16384]"}`},
		{name: "upsert user iterations int32 overflow", method: "POST", path: users, contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256","password":"pw-bob","iterations":2147483648}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "upsert user mechanism alias", method: "POST", path: users, contentType: jsonCT, body: `{"user":"bob","mechanism":"SHA-256","password":"pw-bob"}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/mechanism\": must be one of the allowed values"}`},
		{name: "upsert user without password", method: "POST", path: users, contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256"}`, wantStatus: 400, wantBody: `"error":"request body \"/password\": is required"`},
		{name: "upsert user empty password", method: "POST", path: users, contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256","password":""}`, wantStatus: 400, wantBody: `{"error":"kafka: password is required"}`},
		{name: "upsert user blank user", method: "POST", path: users, contentType: jsonCT, body: `{"user":" ","mechanism":"SCRAM-SHA-256","password":"pw"}`, wantStatus: 400, wantBody: `{"error":"kafka: user is required"}`},
		{name: "upsert user malformed", method: "POST", path: users, contentType: jsonCT, body: `{"user":`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: malformed"}`},
		{name: "upsert user unknown cluster", method: "POST", path: "/api/v1/clusters/nope/users", contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256","password":"pw"}`, wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "upsert user unreachable", method: "POST", path: "/api/v1/clusters/down/users", contentType: jsonCT, body: `{"user":"bob","mechanism":"SCRAM-SHA-256","password":"pw"}`, timeout: timeoutShort, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "users", method: "GET", path: users, wantStatus: 200, wantBody: `{"cluster":"kf","users":[{"user":"alice","credentials":[{"mechanism":"SCRAM-SHA-256","iterations":8192},{"mechanism":"SCRAM-SHA-512","iterations":4096}]},{"user":"bob","credentials":[{"mechanism":"SCRAM-SHA-256","iterations":16384}]}]}`},
		// deleteScramUser
		{name: "delete user one mechanism", method: "DELETE", path: users + "/alice?mechanism=SCRAM-SHA-512", wantStatus: 200, wantBody: `{"deleted":1,"ok":true,"user":"alice"}`},
		{name: "delete user mechanism alias", method: "DELETE", path: users + "/alice?mechanism=sha512", wantStatus: 400, wantBody: `{"code":"invalid_request","error":"parameter \"mechanism\" in query: must be one of the allowed values"}`},
		{name: "delete user mechanism empty", method: "DELETE", path: users + "/alice?mechanism=", wantStatus: 400, wantBody: `"code":"invalid_request"`},
		// Without a mechanism both are tried; one existing is enough.
		{name: "delete user all mechanisms", method: "DELETE", path: users + "/alice", wantStatus: 200, wantBody: `{"deleted":1,"ok":true,"user":"alice"}`},
		{name: "delete user without credentials", method: "DELETE", path: users + "/alice", wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`},
		{name: "delete user blank", method: "DELETE", path: users + "/%20", wantStatus: 400, wantBody: `{"error":"kafka: user is required"}`},
		{name: "delete user unknown cluster", method: "DELETE", path: "/api/v1/clusters/nope/users/alice", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "delete user bob", method: "DELETE", path: users + "/bob", wantStatus: 200, wantBody: `{"deleted":1,"ok":true,"user":"bob"}`},
		{name: "users after delete", method: "GET", path: users, wantStatus: 200, wantBody: `{"cluster":"kf","users":[]}`},
	}
	out := make([]handlerCase, 0, len(cases))
	for _, c := range cases {
		out = append(out, handlerCase{h, c})
	}
	return out
}
