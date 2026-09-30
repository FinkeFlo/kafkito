// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/netguard"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

func TestToAPIError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		status int
		code   string
		msg    string
	}{
		{"apiError", &apiError{Status: 418, Code: "c", Message: "m"}, 418, "c", "m"},
		{"wrapped apiError", fmt.Errorf("x: %w", badRequest("bad")), 400, "", "bad"},
		{"unknown cluster", fmt.Errorf("lookup: %w", kafkapkg.ErrUnknownCluster), 404, "", "unknown cluster"},
		{"no schema registry", kafkapkg.ErrNoSchemaRegistry, 404, "", kafkapkg.ErrNoSchemaRegistry.Error()},
		{"topic not found", kafkapkg.ErrTopicNotFound, 404, "", "topic not found"},
		{"group exists", fmt.Errorf("create g1: %w", kafkapkg.ErrGroupExists), 409, "", "kafka: consumer group already exists"},
		{"not authorized", kafkapkg.ErrNotAuthorized, 403, "", kafkapkg.ErrNotAuthorized.Error()},
		{"value too large", kafkapkg.ErrValueTooLarge, 413, "", fmt.Sprintf("value exceeds the %d MB download limit", kafkapkg.MaxRawDownloadMB)},
		{"value masked", fmt.Errorf("fetch: %w", kafkapkg.ErrValueMasked), 403, "value_masked", "value is masked and cannot be downloaded"},
		{"upstream", upstreamError("list brokers", errors.New("dial broker-7.internal:9092")), 502, "kafka_upstream", "upstream kafka error"},
		{"cluster error unknown", clusterError("c1", "op", kafkapkg.ErrUnknownCluster), 404, "", "unknown cluster: c1"},
		{"cluster error unknown private", clusterError(config.AdhocClusterPrefix+"0123456789abcdef", "op", kafkapkg.ErrUnknownCluster), 404, "", "unknown cluster: " + config.PrivateClusterSentinel},
		{"cluster error upstream", clusterError("c1", "op", errors.New("boom")), 502, "kafka_upstream", "upstream kafka error"},
		{"path not found", routers.ErrPathNotFound, 404, "", "not found"},
		{"method not allowed", routers.ErrMethodNotAllowed, 405, "", "method not allowed"},
		{"generated param", &gen.InvalidParamFormatError{ParamName: "cluster", Err: errors.New("secret-value")}, 400, invalidRequestCode, `invalid parameter "cluster"`},
		{"generated header", &gen.TooManyValuesForParamError{ParamName: "X-Kafkito-Cluster", Count: 2}, 400, invalidRequestCode, `invalid parameter "X-Kafkito-Cluster"`},
		{"unknown", errors.New("internal detail"), 500, "", "internal server error"},
	}
	for _, tc := range cases {
		ae := toAPIError(tc.err)
		assert.Equal(t, tc.status, ae.Status, tc.name)
		assert.Equal(t, tc.code, ae.Code, tc.name)
		assert.Equal(t, tc.msg, ae.Message, tc.name)
	}
	// The sentinel's text is used, not the wrapping chain.
	assert.NotContains(t, toAPIError(fmt.Errorf("group g-secret: %w", kafkapkg.ErrGroupExists)).Message, "g-secret")
}

func TestWriteError(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{}
	ew := errorWriter{log: slog.New(slog.NewJSONHandler(logs, nil))}

	rec := httptest.NewRecorder()
	ew.writeError(rec, httptest.NewRequest(http.MethodGet, "/", nil), upstreamError("list brokers", errors.New("dial broker-7.internal")))
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"error":"upstream kafka error","code":"kafka_upstream"}`, rec.Body.String())
	assert.Contains(t, logs.String(), `"msg":"upstream kafka error"`)
	assert.Contains(t, logs.String(), `"detail":"list brokers"`)
	assert.Contains(t, logs.String(), "broker-7.internal", "the cause is logged")

	rec = httptest.NewRecorder()
	ew.writeError(rec, httptest.NewRequest(http.MethodGet, "/", nil), &apiError{Status: 400, Message: "nope", Err: errors.New("client-input")})
	assert.JSONEq(t, `{"error":"nope"}`, rec.Body.String(), "code is omitted when empty")
	assert.NotContains(t, logs.String(), "client-input", "4xx causes are not logged")

	rec = httptest.NewRecorder()
	ew.writeError(rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("db exploded"))
	assert.JSONEq(t, `{"error":"internal server error"}`, rec.Body.String())
	assert.Contains(t, logs.String(), "db exploded")
}

func TestRequestValidationError_Messages(t *testing.T) {
	t.Parallel()

	const secret = "Sup3r-Secret-Leak-Canary"
	header := &openapi3.Parameter{Name: "X-Kafkito-Cluster", In: "header"}
	body := &openapi3.RequestBody{}
	maxLen := uint64(3)
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"required param", &openapi3filter.RequestError{Parameter: header, Err: openapi3filter.ErrInvalidRequired}, `parameter "X-Kafkito-Cluster" in header: is required`},
		{"empty param", &openapi3filter.RequestError{Parameter: header, Err: openapi3filter.ErrInvalidEmptyValue}, `parameter "X-Kafkito-Cluster" in header: must not be empty`},
		{"param parse error", &openapi3filter.RequestError{Parameter: header, Err: &openapi3filter.ParseError{Kind: openapi3filter.KindInvalidFormat, Value: secret, Reason: "bad " + secret}}, `parameter "X-Kafkito-Cluster" in header: invalid format`},
		{"2020 pattern", &openapi3filter.RequestError{Parameter: header, Err: &openapi3.SchemaError{Reason: "at '': '" + secret + "' does not match pattern '^a$'"}}, `parameter "X-Kafkito-Cluster" in header: must match pattern '^a$'`},
		{"2020 nested enum", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{
			Reason: "jsonschema validation failed\n- at '/auth/type': value must be one of 'a', 'b'",
			Origin: fmt.Errorf("validation failed due to: %w", openapi3.MultiError{&openapi3.SchemaError{Reason: `error at "/auth/type": at '/auth/type': value must be one of 'a', 'b'`}}),
		}}, `request body "/auth/type": must be one of the allowed values`},
		{"2020 const", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `error at "/status": at '/status': value must be 'ok'`}}, `request body "/status": must be the allowed constant value`},
		{"2020 type", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `error at "/scopes": at '/scopes': got string, want null or array`}}, `request body "/scopes": must be of type null or array`},
		{"2020 min items", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `error at "/brokers": at '/brokers': minItems: got 0, want 1`}}, `request body "/brokers": must have at least 1 items`},
		{"2020 maximum", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `error at "/limit": at '/limit': maximum: got 123456, want 500`}}, `request body "/limit": must be <= 500`},
		{"2020 missing", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `at '': missing property 'status'`}}, `request body "/status": is required`},
		{"2020 missing several", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `at '': missing properties 'a', 'b'`}}, `request body: is missing required properties 'a', 'b'`},
		{"2020 additional", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `at '': additional properties '` + secret + `' not allowed`}}, `request body: has properties that are not allowed`},
		{"2020 format", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `at '/url': '` + secret + `' is not valid uri: bad ` + secret}}, `request body "/url": has an invalid format`},
		{"2020 unknown", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{Reason: `at '/x': ` + secret}}, `request body "/x": does not match the schema`},
		{"builtin maxLength", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{SchemaField: "maxLength", Schema: &openapi3.Schema{MaxLength: &maxLen}, Value: secret, Reason: secret}}, `request body: must be at most 3 characters long`},
		{"builtin enum", &openapi3filter.RequestError{RequestBody: body, Err: &openapi3.SchemaError{SchemaField: "enum", Schema: &openapi3.Schema{Enum: []any{"a"}}, Value: secret, Reason: "value " + secret + " is not one of the allowed values"}}, `request body: must be one of the allowed values`},
		{"body required", &openapi3filter.RequestError{RequestBody: body, Err: openapi3filter.ErrInvalidRequired}, `request body: is required`},
		{"content type", &openapi3filter.RequestError{RequestBody: body, Reason: `header Content-Type has unexpected value "` + secret + `"`}, `request body: unsupported Content-Type`},
		{"decode", &openapi3filter.RequestError{RequestBody: body, Reason: "failed to decode request body", Err: errors.New(secret)}, `request body: malformed`},
		{"read", &openapi3filter.RequestError{RequestBody: body, Reason: "reading failed", Err: errors.New(secret)}, `request body: could not be read`},
		{"no location", &openapi3filter.RequestError{Err: errors.New(secret)}, `invalid request`},
		{"security", &openapi3filter.SecurityRequirementsError{Errors: []error{errors.New(secret)}}, `security requirements not met`},
	}
	for _, tc := range cases {
		ae := requestValidationError(tc.err)
		require.NotNil(t, ae, tc.name)
		assert.Equal(t, http.StatusBadRequest, ae.Status, tc.name)
		assert.Equal(t, invalidRequestCode, ae.Code, tc.name)
		assert.Equal(t, tc.want, ae.Message, tc.name)
		assert.NotContains(t, ae.Message, secret, tc.name)
	}

	assert.Nil(t, requestValidationError(errors.New("other")))
}

// A body-limit error surfacing inside a validator error keeps its status.
func TestRequestValidationError_BodyLimitWins(t *testing.T) {
	t.Parallel()

	limit := &apiError{Status: http.StatusRequestEntityTooLarge, Message: "invalid body: http: request body too large"}
	err := &openapi3filter.RequestError{RequestBody: &openapi3.RequestBody{}, Reason: "reading failed", Err: limit}
	assert.Same(t, limit, toAPIError(err))
}

func TestWithCleanURLPath(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	assert.Same(t, r, withCleanURLPath(r))

	r = httptest.NewRequest(http.MethodGet, "/api//v1/./clusters/a%2Fb/", nil)
	c := withCleanURLPath(r)
	assert.Equal(t, "/api/v1/clusters/a/b", c.URL.Path)
	assert.Equal(t, "/api/v1/clusters/a%2Fb", c.URL.EscapedPath())
	assert.Equal(t, "/api//v1/./clusters/a/b/", r.URL.Path, "the original request is untouched")
}

func TestNoRequestBody(t *testing.T) {
	t.Parallel()

	var got []byte
	h := noRequestBody(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.NoBody, r.Body)
		assert.Zero(t, r.ContentLength)
		got = []byte("seen")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader("junk")))
	assert.Equal(t, "seen", string(got))
}

// requestWithCluster is a request as the handler sees it after routing,
// with the cluster path parameter resolved.
func requestWithCluster(cluster string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", cluster)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/__private__/topics", nil)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// #118: the 5xx log names the cluster; requests without one omit the attr.
func TestWriteError_ClusterAttr(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{}
	ew := errorWriter{log: slog.New(slog.NewJSONHandler(logs, nil))}
	ew.writeError(httptest.NewRecorder(), requestWithCluster("prod-eu"), upstreamError("list brokers", errors.New("boom")))
	assert.Contains(t, logs.String(), `"cluster":"prod-eu"`)

	logs2 := &syncBuffer{}
	ew2 := errorWriter{log: slog.New(slog.NewJSONHandler(logs2, nil))}
	ew2.writeError(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/info", nil), errors.New("db exploded"))
	assert.NotContains(t, logs2.String(), `"cluster"`)
}

// blockedDialErr is what franz-go returns when the guard refuses the dial to
// an advertised broker (issue #126, e2e evidence).
func blockedDialErr() error {
	return fmt.Errorf("metadata: unable to dial: %w", &netguard.BlockedAddressError{
		Addr: "localhost:39092", Host: "localhost", IP: netip.MustParseAddr("::1"),
	})
}

func TestUpstreamError_BlockedAddress(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"upstreamError": upstreamError("list topics", blockedDialErr()),
		"clusterError":  clusterError("__adhoc_x", "list topics", blockedDialErr()),
	} {
		ae := toAPIError(err)
		assert.Equal(t, http.StatusBadGateway, ae.Status, name)
		assert.Equal(t, privateClusterAddressBlockedCode, ae.Code, name)
		assert.Equal(t, privateClusterAddressBlockedMsg, ae.Message, name)
		assert.NotContains(t, ae.Message, "localhost", name)
		assert.NotContains(t, ae.Message, "::1", name)
	}
}

func TestWriteError_BlockedAddress(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{}
	ew := errorWriter{log: slog.New(slog.NewJSONHandler(logs, nil))}
	rec := httptest.NewRecorder()

	ew.writeError(rec, requestWithCluster("__adhoc_0123456789abcdef"), upstreamError("list topics", blockedDialErr()))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.JSONEq(t,
		`{"error":"`+privateClusterAddressBlockedMsg+`","code":"private_cluster_address_blocked"}`,
		rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "localhost", "the response never names the refused host")
	// #118: the operator log keeps the cause, host included, and the cluster
	// under its log name.
	assert.Contains(t, logs.String(), `"cluster":"`+config.ClusterLogName("__adhoc_0123456789abcdef")+`"`)
	assert.NotContains(t, logs.String(), config.AdhocClusterPrefix)
	assert.Contains(t, logs.String(), `host \"localhost\" -> ::1`)
}

// blockedDialErrFor wraps a refused dial to host in prefix, the way a store
// error reaches a handler. The refused host is part of the error text.
func blockedDialErrFor(prefix, host string) error {
	return fmt.Errorf("%s: %w", prefix, &netguard.BlockedAddressError{
		Addr: host + ":9092", Host: host, IP: netip.MustParseAddr("::1"),
	})
}

// TestClientErrClassifiers_BlockedAddress pins that the handlers which
// classify store errors by their text never turn a refused dial into a 400:
// the error text names the refused host and IP, and a host may well contain
// a classifier keyword ("operation", "mechanism", "regex", ...). A blocked
// address is always the static 502 (issue #126).
func TestClientErrClassifiers_BlockedAddress(t *testing.T) {
	t.Parallel()

	const aclBody = `{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`
	const topic = "/api/v1/clusters/kf/topics/orders"
	configs := fakeConfigs{"kf": {Name: "kf"}}

	for _, tc := range []struct {
		name, method, path, body string
		host                     string
		st                       func(err error) stores
	}{
		{"createAcl", http.MethodPost, "/api/v1/clusters/kf/acls", aclBody, "operation.broker.example", func(err error) stores {
			return stores{acls: fakeACLs{createACL: func(kafkapkg.ACLSpec) error { return err }}}
		}},
		{"deleteAcl", http.MethodDelete, "/api/v1/clusters/kf/acls", aclBody, "validate.broker.example", func(err error) stores {
			return stores{acls: fakeACLs{deleteACL: func(kafkapkg.ACLSpec) (int, error) { return 0, err }}}
		}},
		{"upsertScramUser", http.MethodPost, "/api/v1/clusters/kf/users", `{"user":"alice","mechanism":"SCRAM-SHA-256","password":"pw-secret"}`, "mechanism.broker.example", func(err error) stores {
			return stores{scram: fakeSCRAM{upsert: func(string, string, string, int32) error { return err }}}
		}},
		{"createGroup", http.MethodPost, "/api/v1/clusters/kf/groups", `{"group_id":"g1","topic":"orders","strategy":"latest"}`, "shift-by.broker.example", func(err error) stores {
			return stores{groups: fakeGroups{createGroup: func(kafkapkg.CreateGroupRequest) (*kafkapkg.ResetOffsetsResult, error) { return nil, err }}}
		}},
		{"resetGroupOffsets", http.MethodPost, "/api/v1/clusters/kf/groups/g1/reset-offsets", `{"topic":"orders","strategy":"latest"}`, "required.broker.example", func(err error) stores {
			return stores{configs: configs, groups: fakeGroups{resetOffsets: func(string, kafkapkg.ResetOffsetsRequest) (*kafkapkg.ResetOffsetsResult, error) { return nil, err }}}
		}},
		{"searchMessages", http.MethodPost, topic + "/messages/search", `{"limit":1,"budget":1}`, "regex.broker.example", func(err error) stores {
			return stores{messages: fakeMessages{search: func(kafkapkg.SearchOptions) (*kafkapkg.SearchResult, error) { return nil, err }}}
		}},
		{"alterTopicConfigs", http.MethodPatch, topic + "/configs", `{"set":{"retention.ms":"1000"}}`, "required.broker.example", func(err error) stores {
			return stores{topics: fakeTopics{writeErr: err}}
		}},
		{"deleteRecords", http.MethodDelete, topic + "/records", `{"partitions":{"0":0}}`, "required.broker.example", func(err error) stores {
			return stores{configs: configs, topics: fakeTopics{writeErr: err}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fakeServer(t, tc.st(blockedDialErrFor("unable to dial", tc.host)))
			req, rec := requestCase{method: tc.method, path: tc.path, contentType: "application/json", body: tc.body}.do(t, h)
			assert.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
			assert.JSONEq(t,
				`{"error":"`+privateClusterAddressBlockedMsg+`","code":"private_cluster_address_blocked"}`,
				rec.Body.String())
			assert.NotContains(t, rec.Body.String(), tc.host)
			assertResponseMatchesSpec(t, contractRouter(t), req, rec)
		})
	}

	// createTopic's only text rule is "topic name required", which no host
	// name can carry; the prefix does, to pin that the guard runs first.
	t.Run("createTopic", func(t *testing.T) {
		t.Parallel()
		h := fakeServer(t, stores{topics: fakeTopics{writeErr: blockedDialErrFor("topic name required", "broker.example")}})
		_, rec := requestCase{method: http.MethodPost, path: "/api/v1/clusters/kf/topics", contentType: "application/json", body: `{"name":"t1"}`}.do(t, h)
		assert.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "broker.example")
	})
}

// TestProduceError_BlockedAddress pins the produce classifier: a refused
// dial is the static 502 even when its text matches a produce input rule.
func TestProduceError_BlockedAddress(t *testing.T) {
	t.Parallel()

	partition := int32(3)
	for name, err := range map[string]error{
		"invalid partition": blockedDialErrFor("invalid record partitioning choice", "broker.example"),
		"client input":      blockedDialErrFor("produce", "unsupported encoding.example"),
		"not authorized":    blockedDialErrFor("TOPIC_AUTHORIZATION_FAILED", "broker.example"),
	} {
		ae := toAPIError(produceError("kf", "orders", &partition, err))
		assert.Equal(t, http.StatusBadGateway, ae.Status, name)
		assert.Equal(t, privateClusterAddressBlockedCode, ae.Code, name)
		assert.Equal(t, privateClusterAddressBlockedMsg, ae.Message, name)
	}
}
