// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
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
		{"paramError", badParam("invalid limit"), 400, "", "invalid limit"},
		{"unknown cluster", fmt.Errorf("lookup: %w", kafkapkg.ErrUnknownCluster), 404, "", "unknown cluster"},
		{"no schema registry", kafkapkg.ErrNoSchemaRegistry, 404, "", kafkapkg.ErrNoSchemaRegistry.Error()},
		{"topic not found", kafkapkg.ErrTopicNotFound, 404, "", "topic not found"},
		{"group exists", fmt.Errorf("create g1: %w", kafkapkg.ErrGroupExists), 409, "", "kafka: consumer group already exists"},
		{"not authorized", kafkapkg.ErrNotAuthorized, 403, "", kafkapkg.ErrNotAuthorized.Error()},
		{"value too large", kafkapkg.ErrValueTooLarge, 413, "", fmt.Sprintf("value exceeds the %d MB download limit", kafkapkg.MaxRawDownloadMB)},
		{"upstream", upstreamError("list brokers", errors.New("dial broker-7.internal:9092")), 502, "kafka_upstream", "upstream kafka error"},
		{"cluster error unknown", clusterError("c1", "op", kafkapkg.ErrUnknownCluster), 404, "", "unknown cluster: c1"},
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
