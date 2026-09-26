// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

func TestIsSCRAMClientErr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "required field missing", msg: "user required", want: true},
		{name: "mechanism keyword", msg: "unsupported mechanism: PLAIN", want: true},
		{name: "iterations keyword", msg: "iterations must be >= 4096", want: true},
		{name: "broker network error", msg: "dial tcp broker-host:9092: connection refused", want: false},
		{name: "timeout", msg: "context deadline exceeded", want: false},
		{name: "sasl auth failure", msg: "SASL authentication failed", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isSCRAMClientErr(tc.msg), "isSCRAMClientErr(%q)", tc.msg)
		})
	}
}

// TestUpsertScramUser_ErrorMapping pins how upsertScramUser maps store
// errors, and that the password never reaches the response.
func TestUpsertScramUser_ErrorMapping(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"rejected input", errors.New("iterations 99 out of range [4096,16384]"), http.StatusBadRequest, `{"error":"kafka: iterations 99 out of range [4096,16384]"}`},
		{"unknown cluster", fmt.Errorf("mechanism lookup: %w", kafkapkg.ErrUnknownCluster), http.StatusNotFound, `{"error":"unknown cluster: kf"}`},
		{"broker failure", errBroker, http.StatusBadGateway, upstreamBody},
		{"success", nil, http.StatusOK, `{"mechanism":"SCRAM-SHA-512","ok":true,"user":"alice"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotPassword string
			var gotIterations int32
			h := fakeServer(t, stores{scram: fakeSCRAM{upsert: func(_, _, password string, iterations int32) error {
				gotPassword, gotIterations = password, iterations
				return tc.err
			}}})
			req := requestCase{
				method: http.MethodPost, path: "/api/v1/clusters/kf/users", contentType: "application/json",
				body: `{"user":"alice","mechanism":"SCRAM-SHA-512","password":"pw-secret","iterations":8192}`,
			}
			_, rec := req.do(t, h)
			assert.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantBody+"\n", rec.Body.String())
			assert.NotContains(t, rec.Body.String(), "pw-secret")
			assert.Equal(t, "pw-secret", gotPassword)
			assert.EqualValues(t, 8192, gotIterations)
		})
	}
}

// TestDeleteScramUser_Mechanisms pins that deleteScramUser tries both
// mechanisms when the query names none, succeeds if one credential was
// deleted, and otherwise maps the last error.
func TestDeleteScramUser_Mechanisms(t *testing.T) {
	t.Parallel()

	const path = "/api/v1/clusters/kf/users/alice"
	for _, tc := range []struct {
		name       string
		query      string
		errs       map[string]error // by mechanism
		wantCalls  []string
		wantStatus int
		wantBody   string
	}{
		{name: "both deleted", wantCalls: []string{"SCRAM-SHA-256", "SCRAM-SHA-512"}, wantStatus: http.StatusOK, wantBody: `{"deleted":2,"ok":true,"user":"alice"}`},
		{name: "one deleted", errs: map[string]error{"SCRAM-SHA-256": errBroker}, wantCalls: []string{"SCRAM-SHA-256", "SCRAM-SHA-512"}, wantStatus: http.StatusOK, wantBody: `{"deleted":1,"ok":true,"user":"alice"}`},
		{name: "none deleted, last error rejected input", errs: map[string]error{"SCRAM-SHA-256": errBroker, "SCRAM-SHA-512": errors.New("mechanism SCRAM-SHA-512 has no credential")}, wantCalls: []string{"SCRAM-SHA-256", "SCRAM-SHA-512"}, wantStatus: http.StatusBadRequest, wantBody: `{"error":"kafka: mechanism SCRAM-SHA-512 has no credential"}`},
		{name: "none deleted, last error broker failure", errs: map[string]error{"SCRAM-SHA-256": errors.New("mechanism missing"), "SCRAM-SHA-512": errBroker}, wantCalls: []string{"SCRAM-SHA-256", "SCRAM-SHA-512"}, wantStatus: http.StatusBadGateway, wantBody: upstreamBody},
		{name: "named mechanism", query: "?mechanism=SCRAM-SHA-512", wantCalls: []string{"SCRAM-SHA-512"}, wantStatus: http.StatusOK, wantBody: `{"deleted":1,"ok":true,"user":"alice"}`},
		{name: "named mechanism fails", query: "?mechanism=SCRAM-SHA-512", errs: map[string]error{"SCRAM-SHA-512": kafkapkg.ErrUnknownCluster}, wantCalls: []string{"SCRAM-SHA-512"}, wantStatus: http.StatusNotFound, wantBody: `{"error":"unknown cluster: kf"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			h := fakeServer(t, stores{scram: fakeSCRAM{delete: func(user, mechanism string) error {
				assert.Equal(t, "alice", user)
				calls = append(calls, mechanism)
				return tc.errs[mechanism]
			}}})
			runFakeCases(t, h, []requestCase{{
				name: tc.name, method: http.MethodDelete, path: path + tc.query,
				wantStatus: tc.wantStatus, wantBody: tc.wantBody,
			}})
			assert.Equal(t, tc.wantCalls, calls)
		})
	}
}
