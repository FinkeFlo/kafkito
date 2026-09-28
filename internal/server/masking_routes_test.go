// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// Masked values are neither returned by the raw download nor searchable in
// clear text; topics without a rule are unaffected.
func TestMaskedValues_RawDownloadAndSearch(t *testing.T) {
	t.Parallel()
	addr := startKfake(t, "secrets", "plain")
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{
		Name:    "kf",
		Brokers: []string{addr},
		DataMasking: []config.MaskingRule{{
			Topics: []string{"^secrets$"},
			Fields: []string{"$.email"},
		}},
	}}, slog.Default())
	t.Cleanup(reg.Close)
	h := New(Options{Version: "v-test", Logger: slog.Default(), Registry: reg, Config: config.Defaults()})
	const (
		base   = "/api/v1/clusters/kf/topics"
		jsonCT = "application/json"
		value  = `{"value":"{\"id\":1,\"email\":\"alice@example.com\"}"}`
	)

	cases := []requestCase{
		{name: "produce masked", method: http.MethodPost, path: base + "/secrets/messages", contentType: jsonCT, body: value, wantStatus: 200},
		{name: "produce plain", method: http.MethodPost, path: base + "/plain/messages", contentType: jsonCT, body: value, wantStatus: 200},
		{name: "consume masked", method: http.MethodGet, path: base + "/secrets/messages?from=start", wantStatus: 200, wantBody: `"masked":true`},
		{name: "raw masked", method: http.MethodGet, path: base + "/secrets/messages/0/0/raw", wantStatus: 403,
			wantBody:   `{"code":"value_masked","error":"value is masked and cannot be downloaded"}`,
			wantHeader: map[string]string{"Content-Type": "application/json", "Content-Disposition": ""}},
		{name: "raw plain", method: http.MethodGet, path: base + "/plain/messages/0/0/raw", wantStatus: 200, wantBody: "alice@example.com"},
		{name: "search masked for hidden text", method: http.MethodPost, path: base + "/secrets/messages/search", contentType: jsonCT,
			body: `{"mode":"contains","value":"alice","direction":"oldest_first"}`, wantStatus: 200, wantBody: `"matched":0,`},
		{name: "search masked jsonpath", method: http.MethodPost, path: base + "/secrets/messages/search", contentType: jsonCT,
			body: `{"mode":"jsonpath","path":"$.email","op":"contains","value":"@","direction":"oldest_first"}`, wantStatus: 200, wantBody: `"matched":0,`},
		{name: "search masked visible text", method: http.MethodPost, path: base + "/secrets/messages/search", contentType: jsonCT,
			body: `{"mode":"contains","value":"\"id\":1","direction":"oldest_first"}`, wantStatus: 200, wantBody: `"masked":true`},
		{name: "search plain", method: http.MethodPost, path: base + "/plain/messages/search", contentType: jsonCT,
			body: `{"mode":"contains","value":"alice","direction":"oldest_first"}`, wantStatus: 200, wantBody: `alice@example.com`},
	}

	router := contractRouter(t)
	for _, tc := range cases {
		req, rec := tc.do(t, h)
		require.Equal(t, tc.wantStatus, rec.Code, "%s: %s", tc.name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), tc.wantBody, tc.name)
		for k, v := range tc.wantHeader {
			assert.Equal(t, v, rec.Header().Get(k), "%s: header %s", tc.name, k)
		}
		if strings.Contains(tc.path, "/secrets/") {
			assert.NotContains(t, rec.Body.String(), "alice", "%s leaks the masked value", tc.name)
		}
		assertResponseMatchesSpec(t, router, req, rec)
	}
}

// keyHeaderMaskingServer serves a kfake cluster "kf" whose topic "secrets"
// has its key and authorization header masked, and whose topic "legacy" has
// a value rule without targets.
func keyHeaderMaskingServer(t *testing.T) http.Handler {
	t.Helper()
	addr := startKfake(t, "secrets", "legacy", "dest")
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{
		Name:    "kf",
		Brokers: []string{addr},
		DataMasking: []config.MaskingRule{
			{Topics: []string{"^secrets$"}, Targets: []string{"key"}, Regex: []config.RegexMask{{Match: `cust-\d+`, Replacement: "cust-***"}}},
			{Topics: []string{"^secrets$"}, Targets: []string{"headers"}, Headers: []string{"^authorization$"}, Regex: []config.RegexMask{{Match: `.+`}}},
			{Topics: []string{"^legacy$"}, Fields: []string{"$.email"}},
		},
	}}, slog.Default())
	t.Cleanup(reg.Close)
	return New(Options{Version: "v-test", Logger: slog.Default(), Registry: reg, Config: config.Defaults()})
}

// Masked keys and header values never leave the server in clear text: not
// in consume, sample or search responses, not via a search match, and not
// through a topic copy. Not parallel: the copy semaphore is package-level
// (see topic_copy_stream_test.go).
func TestMaskedKeysAndHeaders_NoClearTextOnAnyPath(t *testing.T) {
	h := keyHeaderMaskingServer(t)
	const (
		base   = "/api/v1/clusters/kf/topics"
		jsonCT = "application/json"
		record = `{"key":"cust-4711","value":"{\"order\":\"o-1\"}","headers":{"authorization":"Bearer tok-9f8e","trace-id":"trace-visible"}}`
	)
	secrets := []string{"4711", "tok-9f8e", "Bearer"}

	cases := []requestCase{
		{name: "produce", method: http.MethodPost, path: base + "/secrets/messages", contentType: jsonCT, body: record, wantStatus: 200},
		{name: "consume", method: http.MethodGet, path: base + "/secrets/messages?from=start", wantStatus: 200,
			wantBody: `"key":"cust-***","key_encoding":"text"`},
		{name: "consume flags", method: http.MethodGet, path: base + "/secrets/messages?from=start", wantStatus: 200,
			wantBody: `"key_masked":true,"masked_headers":["authorization"]`},
		{name: "consume visible header", method: http.MethodGet, path: base + "/secrets/messages?from=start", wantStatus: 200,
			wantBody: `"trace-id":"trace-visible"`},
		{name: "sample", method: http.MethodGet, path: base + "/secrets/sample", wantStatus: 200, wantBody: `"key_masked":true`},
		{name: "search key", method: http.MethodPost, path: base + "/secrets/messages/search", contentType: jsonCT,
			body: `{"mode":"contains","value":"4711","zones":["key"],"direction":"oldest_first"}`, wantStatus: 200, wantBody: `"matched":0,`},
		{name: "search header", method: http.MethodPost, path: base + "/secrets/messages/search", contentType: jsonCT,
			body: `{"mode":"contains","value":"tok-9f8e","zones":["headers"],"direction":"oldest_first"}`, wantStatus: 200, wantBody: `"matched":0,`},
		{name: "search js", method: http.MethodPost, path: base + "/secrets/messages/search", contentType: jsonCT,
			body: `{"mode":"js","value":"key.includes('4711') || (headers && headers.authorization.includes('tok'))","direction":"oldest_first"}`, wantStatus: 200, wantBody: `"matched":0,`},
		{name: "search masked form", method: http.MethodPost, path: base + "/secrets/messages/search", contentType: jsonCT,
			body: `{"mode":"contains","value":"cust-***","zones":["key"],"direction":"oldest_first"}`, wantStatus: 200, wantBody: `"key_masked":true`},
		{name: "raw value only", method: http.MethodGet, path: base + "/secrets/messages/0/0/raw", wantStatus: 200, wantBody: `{"order":"o-1"}`},
		{name: "copy skips", method: http.MethodPost, path: base + "/secrets/copy", contentType: jsonCT,
			body: `{"dest_cluster":"kf","dest_topic":"dest"}`, wantStatus: 200, wantBody: `"skipped":1`},
		{name: "dest stays empty", method: http.MethodGet, path: base + "/dest/messages?from=start", wantStatus: 200, wantBody: `"messages":[]`},
	}

	router := contractRouter(t)
	for _, tc := range cases {
		req, rec := tc.do(t, h)
		require.Equal(t, tc.wantStatus, rec.Code, "%s: %s", tc.name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), tc.wantBody, tc.name)
		if tc.name != "produce" {
			for _, s := range secrets {
				assert.NotContains(t, rec.Body.String(), s, "%s leaks %q", tc.name, s)
			}
		}
		if tc.name != "copy skips" {
			assertResponseMatchesSpec(t, router, req, rec)
		}
	}
	assert.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond, "copy slot released")
}

// A rule without targets masks the value only, exactly as before: key and
// headers are returned, searched and copied as they are.
func TestMaskingRuleWithoutTargets_KeyAndHeadersUnchanged(t *testing.T) {
	h := keyHeaderMaskingServer(t)
	const (
		base   = "/api/v1/clusters/kf/topics"
		jsonCT = "application/json"
		record = `{"key":"cust-4711","value":"{\"email\":\"alice@example.com\"}","headers":{"authorization":"Bearer tok-9f8e"}}`
	)
	cases := []requestCase{
		{name: "produce", method: http.MethodPost, path: base + "/legacy/messages", contentType: jsonCT, body: record, wantStatus: 200},
		{name: "consume", method: http.MethodGet, path: base + "/legacy/messages?from=start", wantStatus: 200,
			wantBody: `"key":"cust-4711"`},
		{name: "consume header", method: http.MethodGet, path: base + "/legacy/messages?from=start", wantStatus: 200,
			wantBody: `"authorization":"Bearer tok-9f8e"`},
		{name: "search key", method: http.MethodPost, path: base + "/legacy/messages/search", contentType: jsonCT,
			body: `{"mode":"contains","value":"4711","zones":["key"],"direction":"oldest_first"}`, wantStatus: 200, wantBody: `"matched":1,`},
	}
	router := contractRouter(t)
	for _, tc := range cases {
		req, rec := tc.do(t, h)
		require.Equal(t, tc.wantStatus, rec.Code, "%s: %s", tc.name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), tc.wantBody, tc.name)
		assert.NotContains(t, rec.Body.String(), "key_masked", tc.name)
		assert.NotContains(t, rec.Body.String(), "masked_headers", tc.name)
		assert.NotContains(t, rec.Body.String(), "alice", tc.name)
		assertResponseMatchesSpec(t, router, req, rec)
	}
}
