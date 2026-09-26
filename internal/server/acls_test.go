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

func TestIsACLClientErr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "required field missing", msg: "principal required", want: true},
		{name: "validate keyword", msg: "validate: host must not be empty", want: true},
		{name: "resource_type keyword", msg: "unknown resource_type: Foo", want: true},
		{name: "pattern_type keyword", msg: "invalid pattern_type: Bar", want: true},
		{name: "operation keyword", msg: "unsupported operation: WRITE", want: true},
		{name: "permission_type keyword", msg: "unknown permission_type: DENY", want: true},
		{name: "broker network error", msg: "dial tcp broker-host:9092: connection refused", want: false},
		{name: "leader not available", msg: "leader not available", want: false},
		{name: "timeout", msg: "context deadline exceeded", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isACLClientErr(tc.msg), "isACLClientErr(%q)", tc.msg)
		})
	}
}

// TestACLWrites_ErrorMapping pins how createAcl and deleteAcl map store
// errors: rejected input is a 400 naming the reason, an unknown cluster a
// 404 even if its text looks like rejected input, anything else an opaque
// 502.
func TestACLWrites_ErrorMapping(t *testing.T) {
	t.Parallel()

	const body = `{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"rejected input", errors.New("validate: host must not be empty"), http.StatusBadRequest, `{"error":"kafka: validate: host must not be empty"}`},
		{"unknown cluster", fmt.Errorf("operation on %w", kafkapkg.ErrUnknownCluster), http.StatusNotFound, `{"error":"unknown cluster: kf"}`},
		{"broker failure", errBroker, http.StatusBadGateway, upstreamBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fakeServer(t, stores{acls: fakeACLs{
				createACL: func(kafkapkg.ACLSpec) error { return tc.err },
				deleteACL: func(kafkapkg.ACLSpec) (int, error) { return 0, tc.err },
			}})
			runFakeCases(t, h, []requestCase{
				{name: "create", method: http.MethodPost, path: "/api/v1/clusters/kf/acls", contentType: "application/json", body: body, wantStatus: tc.wantStatus, wantBody: tc.wantBody},
				{name: "delete", method: http.MethodDelete, path: "/api/v1/clusters/kf/acls", contentType: "application/json", body: body, wantStatus: tc.wantStatus, wantBody: tc.wantBody},
			})
		})
	}
}

// TestDeleteAcl_ReturnsDeletedCount pins that deleteAcl passes the filter
// to the store and reports how many bindings it removed.
func TestDeleteAcl_ReturnsDeletedCount(t *testing.T) {
	t.Parallel()

	var got kafkapkg.ACLSpec
	h := fakeServer(t, stores{acls: fakeACLs{deleteACL: func(spec kafkapkg.ACLSpec) (int, error) {
		got = spec
		return 3, nil
	}}})
	runFakeCases(t, h, []requestCase{{
		name: "delete", method: http.MethodDelete, path: "/api/v1/clusters/kf/acls", contentType: "application/json",
		body:       `{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`,
		wantStatus: http.StatusOK, wantBody: `{"deleted":3,"ok":true}`,
	}})
	assert.Equal(t, kafkapkg.ACLSpec{Principal: "User:alice", Host: "*", ResourceType: "TOPIC", ResourceName: "orders", PatternType: "LITERAL", Operation: "READ", PermissionType: "ALLOW"}, got)
}
