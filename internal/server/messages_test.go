// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isSearchClientErr(tc.msg), "isSearchClientErr(%q)", tc.msg)
		})
	}
}
