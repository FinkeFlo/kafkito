// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
