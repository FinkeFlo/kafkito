// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// schemaOps are one request per schema operation.
var schemaOps = []requestCase{
	{name: "list subjects", method: http.MethodGet, path: "/api/v1/clusters/kf/schemas/subjects"},
	{name: "list versions", method: http.MethodGet, path: "/api/v1/clusters/kf/schemas/subjects/orders-value/versions"},
	{name: "get version", method: http.MethodGet, path: "/api/v1/clusters/kf/schemas/subjects/orders-value/versions/latest"},
	{name: "register", method: http.MethodPost, path: "/api/v1/clusters/kf/schemas/subjects/orders-value/versions", contentType: "application/json", body: `{"schema":"\"string\""}`},
	{name: "delete subject", method: http.MethodDelete, path: "/api/v1/clusters/kf/schemas/subjects/orders-value"},
}

// TestSchemaOps_ClientErrors pins how every schema operation maps a failure
// to get the cluster's Schema Registry client.
func TestSchemaOps_ClientErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"no schema registry", kafkapkg.ErrNoSchemaRegistry, http.StatusNotFound, `{"error":"schema registry not configured for cluster: kf"}`},
		{"unknown cluster", kafkapkg.ErrUnknownCluster, http.StatusNotFound, `{"error":"unknown cluster: kf"}`},
		{"other failure", errBroker, http.StatusBadGateway, upstreamBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fakeServer(t, stores{schemas: fakeSchemas{err: tc.err}})
			runFakeCases(t, h, withWant(schemaOps, tc.wantStatus, tc.wantBody))
		})
	}
}

// TestSchemaOps_UpstreamErrors pins that a failed Schema Registry call is
// an opaque 502 on every operation, whatever the registry answered.
func TestSchemaOps_UpstreamErrors(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"registry error":      fmt.Errorf("sr: %w", &kafkapkg.SRError{Status: http.StatusNotFound, Code: 40401, Message: "Subject 'orders-value' not found at http://10.0.0.8:8081"}),
		"insecure basic auth": kafkapkg.ErrInsecureSchemaRegistryAuth,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := fakeServer(t, stores{schemas: fakeSchemas{client: fakeSchemaClient{err: err}}})
			runFakeCases(t, h, withWant(schemaOps, http.StatusBadGateway, upstreamBody))
		})
	}
}

// TestListSubjects_SortsByName pins that subjects are returned sorted by
// name whatever order the registry used.
func TestListSubjects_SortsByName(t *testing.T) {
	t.Parallel()

	h := fakeServer(t, stores{schemas: fakeSchemas{client: fakeSchemaClient{subjects: []kafkapkg.Subject{
		{Name: "payments-value", Versions: []int{1}},
		{Name: "orders-value", Versions: []int{1, 2}},
	}}}})
	runFakeCases(t, h, []requestCase{{
		name: "sorted", method: http.MethodGet, path: "/api/v1/clusters/kf/schemas/subjects",
		wantStatus: http.StatusOK,
		wantBody:   `{"cluster":"kf","subjects":[{"name":"orders-value","versions":[1,2]},{"name":"payments-value","versions":[1]}]}`,
	}})
}

// TestDeleteSubject_PermanentFlag pins that deleteSubject passes the
// permanent flag through and rejects spellings other than true and false
// before calling the registry.
func TestDeleteSubject_PermanentFlag(t *testing.T) {
	t.Parallel()

	type call struct {
		subject   string
		permanent bool
	}
	var calls []call
	h := fakeServer(t, stores{schemas: fakeSchemas{client: fakeSchemaClient{deleted: func(subject string, permanent bool) {
		calls = append(calls, call{subject, permanent})
	}}}})
	const path = "/api/v1/clusters/kf/schemas/subjects/orders-value"
	runFakeCases(t, h, []requestCase{
		{name: "soft by default", method: http.MethodDelete, path: path, wantStatus: http.StatusOK, wantBody: `"permanent":false`},
		{name: "permanent", method: http.MethodDelete, path: path + "?permanent=true", wantStatus: http.StatusOK, wantBody: `"permanent":true`},
		{name: "permanent spelled 1", method: http.MethodDelete, path: path + "?permanent=1", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
	})
	assert.Equal(t, []call{{"orders-value", false}, {"orders-value", true}}, calls)
}

// withWant returns cases with the given expected status and body.
func withWant(cases []requestCase, status int, body string) []requestCase {
	out := make([]requestCase, len(cases))
	for i, c := range cases {
		c.wantStatus, c.wantBody = status, body
		out[i] = c
	}
	return out
}
