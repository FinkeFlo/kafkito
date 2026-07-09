// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import "testing"

func TestIsAuthorizationFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "group", msg: "GROUP_AUTHORIZATION_FAILED: Group authorization failed.", want: true},
		{name: "topic", msg: "list offsets for topic \"x\": TOPIC_AUTHORIZATION_FAILED", want: true},
		{name: "cluster", msg: "describe acls filter: CLUSTER_AUTHORIZATION_FAILED", want: true},
		{name: "network error", msg: "dial tcp broker:9092: connection refused", want: false},
		{name: "not found", msg: "topic \"x\" not found", want: false},
		{name: "empty", msg: "", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isAuthorizationFailure(tc.msg); got != tc.want {
				t.Fatalf("isAuthorizationFailure(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
