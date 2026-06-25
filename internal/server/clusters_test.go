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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isACLClientErr(tc.msg), "isACLClientErr(%q)", tc.msg)
		})
	}
}

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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isSCRAMClientErr(tc.msg), "isSCRAMClientErr(%q)", tc.msg)
		})
	}
}

func TestIsSearchClientErr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "jsonpath keyword", msg: "invalid jsonpath expression: $.[", want: true},
		{name: "xpath keyword", msg: "xpath compile error", want: true},
		{name: "regex keyword", msg: "invalid regex: [unclosed", want: true},
		{name: "numeric op keyword", msg: "numeric op requires a number value", want: true},
		{name: "unknown search mode", msg: "unknown search mode: fuzzy", want: true},
		{name: "unknown operator", msg: "unknown operator: between", want: true},
		{name: "js filter keyword", msg: "js filter compile error", want: true},
		{name: "broker network error", msg: "dial tcp broker-host:9092: connection refused", want: false},
		{name: "timeout", msg: "context deadline exceeded", want: false},
		{name: "topic not found", msg: "topic does not exist", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isSearchClientErr(tc.msg), "isSearchClientErr(%q)", tc.msg)
		})
	}
}
