// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
