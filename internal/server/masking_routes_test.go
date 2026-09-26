// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

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
