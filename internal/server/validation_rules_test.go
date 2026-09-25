// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// The rule texts of validation errors are rebuilt from kin-openapi's error
// structure and, for OpenAPI 3.1, from the message strings of its JSON Schema
// 2020-12 validator. These tests run real requests through the real
// validator middleware and pin the exact texts, so a dependency upgrade that
// changes the messages fails here instead of silently degrading them to a
// generic fallback.

// rulesSpec is a test-only OpenAPI 3.1 document. Its body schema has no
// $ref, so kin-openapi validates it with the JSON Schema 2020-12 validator.
const rulesSpec = `openapi: 3.1.0
info: {title: rules, version: "1"}
paths:
  /rules:
    post:
      operationId: rules
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string, minLength: 2}
                mode: {type: string, enum: [a, b]}
                count: {type: integer, maximum: 10}
                slug: {type: string, pattern: '^[a-z]+$'}
                tags: {type: array, minItems: 1, items: {type: string}}
                kind: {const: fixed}
                scopes: {type: [array, "null"], items: {type: string}}
                nested:
                  type: object
                  required: [id]
                  properties:
                    id: {type: string}
      responses:
        "204": {description: ok}
`

// ruleValue is submitted as the offending value wherever the rule allows it;
// it must never appear in a response.
const ruleValue = "Rule-Value-Leak-Canary"

func TestRequestValidator_RuleTexts_JSONSchema2020(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromData([]byte(rulesSpec))
	require.NoError(t, err)
	logs := &syncBuffer{}
	validate := newDocValidator(doc, errorWriter{log: slog.New(slog.NewJSONHandler(logs, nil))})
	h := validate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name, body, want string
	}{
		{"valid", `{"name":"ok"}`, ""},
		{"nullable accepts null", `{"name":"ok","scopes":null}`, ""},
		{"nullable accepts array", `{"name":"ok","scopes":["s"]}`, ""},
		{"const accepts value", `{"name":"ok","kind":"fixed"}`, ""},
		{"enum", `{"name":"ok","mode":"` + ruleValue + `"}`, `request body "/mode": must be one of the allowed values`},
		{"type", `{"name":"ok","count":"` + ruleValue + `"}`, `request body "/count": must be of type integer`},
		{"maximum", `{"name":"ok","count":11}`, `request body "/count": must be <= 10`},
		{"minLength", `{"name":"x"}`, `request body "/name": must be at least 2 characters long`},
		{"pattern", `{"name":"ok","slug":"` + ruleValue + `"}`, `request body "/slug": must match pattern '^[a-z]+$'`},
		{"minItems", `{"name":"ok","tags":[]}`, `request body "/tags": must have at least 1 items`},
		{"required", `{"mode":"a"}`, `request body "/name": is required`},
		{"nested required", `{"name":"ok","nested":{}}`, `request body "/nested/id": is required`},
		{"const", `{"name":"ok","kind":"` + ruleValue + `"}`, `request body "/kind": must be the allowed constant value`},
		{"nullable rejects other types", `{"name":"ok","scopes":"` + ruleValue + `"}`, `request body "/scopes": must be of type null or array`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/rules", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if tc.want == "" {
				assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
				return
			}
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.JSONEq(t, `{"code":"invalid_request","error":`+jsonString(t, tc.want)+`}`, rec.Body.String())
			assert.Empty(t, schemaFieldOf(t, doc, "/rules", tc.body),
				"the 2020-12 validator (message-string errors) must produce this error")
		})
	}
	assert.NotContains(t, logs.String(), ruleValue)
}

// The embedded spec's ClusterConfig contains $ref, which kin-openapi's JSON
// Schema 2020-12 compiler rejects, so it falls back to the built-in
// validator (structured errors). Its rule texts must match the 2020 ones.
func TestRequestValidator_RuleTexts_EmbeddedSpec(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	t.Cleanup(reg.Close)
	logs := &syncBuffer{}
	h := New(Options{Version: "test", Logger: slog.New(slog.NewJSONHandler(logs, nil)), Registry: reg, Config: config.Defaults()})
	doc, err := loadSpec()
	require.NoError(t, err)

	for _, tc := range []struct {
		name, body, want string
	}{
		{"enum", `{"brokers":["h:1"],"auth":{"type":"` + ruleValue + `"}}`, `request body "/auth/type": must be one of the allowed values`},
		{"enum is case-sensitive", `{"brokers":["h:1"],"auth":{"type":"PLAIN"}}`, `request body "/auth/type": must be one of the allowed values`},
		{"type", `{"brokers":["h:1"],"tls":{"enabled":"` + ruleValue + `"}}`, `request body "/tls/enabled": must be of type boolean`},
		{"minItems", `{"brokers":[]}`, `request body "/brokers": must have at least 1 items`},
		{"required", `{"auth":{"type":"none"}}`, `request body "/brokers": is required`},
		{"nullable rejects other types", `{"brokers":["h:1"],"data_masking":"` + ruleValue + `"}`, `request body "/data_masking": must be of type array or null`},
		{"nullable items", `{"brokers":["h:1"],"data_masking":[1]}`, `request body "/data_masking/0": must be of type object`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.JSONEq(t, `{"code":"invalid_request","error":`+jsonString(t, tc.want)+`}`, rec.Body.String())
			assert.NotEmpty(t, schemaFieldOf(t, doc, "/api/v1/clusters/_test", tc.body),
				"the built-in validator (structured errors) must produce this error")
		})
	}
	assert.NotContains(t, logs.String(), ruleValue)
}

// schemaFieldOf validates a JSON POST body against doc directly and returns
// the SchemaField of the innermost schema error, which is empty for errors of
// the JSON Schema 2020-12 validator.
func schemaFieldOf(t *testing.T, doc *openapi3.T, path, body string) string {
	t.Helper()
	cp := *doc
	cp.Servers = nil
	router, err := gorillamux.NewRouter(&cp)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	route, params, err := router.FindRoute(req)
	require.NoError(t, err)
	err = openapi3filter.ValidateRequest(context.Background(), &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	})
	var se *openapi3.SchemaError
	require.ErrorAs(t, err, &se)
	return innermostSchemaError(se).SchemaField
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}
