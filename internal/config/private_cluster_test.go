// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAdhocClusterName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want bool
	}{
		{AdhocClusterPrefix + "0123456789abcdef", true},
		{AdhocClusterPrefix, true},
		{"", false},
		{PrivateClusterSentinel, false},
		{"prod", false},
		{"x" + AdhocClusterPrefix + "0123456789abcdef", false},
		{"__ADHOC_0123456789abcdef", false},
	} {
		assert.Equal(t, tc.want, IsAdhocClusterName(tc.name), "%q", tc.name)
	}
}

func TestValidate_PrivateClusterNamesReserved(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		reserved bool
	}{
		{PrivateClusterSentinel, true},
		{AdhocClusterPrefix + "0123456789abcdef", true},
		{AdhocClusterPrefix, true},
		{"prod", false},
		{"adhoc", false},
	} {
		err := Config{Clusters: []ClusterConfig{{Name: tc.name, Brokers: []string{"b:9092"}}}}.Validate()
		if tc.reserved {
			assert.ErrorContains(t, err, "reserved for private clusters", tc.name)
		} else {
			assert.NoError(t, err, tc.name)
		}
	}
}

func TestPublicClusterName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, want string }{
		{AdhocClusterPrefix + "0123456789abcdef", PrivateClusterSentinel},
		{AdhocClusterPrefix, PrivateClusterSentinel},
		{PrivateClusterSentinel, PrivateClusterSentinel},
		{"prod", "prod"},
		{"", ""},
		{"x" + AdhocClusterPrefix + "0123456789abcdef", "x" + AdhocClusterPrefix + "0123456789abcdef"},
	} {
		assert.Equal(t, tc.want, PublicClusterName(tc.name), "%q", tc.name)
	}
}
