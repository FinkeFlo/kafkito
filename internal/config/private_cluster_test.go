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

func TestClusterLogName(t *testing.T) {
	t.Parallel()

	internal := AdhocClusterPrefix + "0123456789abcdef"
	for _, tc := range []struct{ name, want string }{
		{internal, "private-0f3f15bc7c80"},
		{"prod", "prod"},
		{PrivateClusterSentinel, PrivateClusterSentinel},
		{"", ""},
	} {
		assert.Equal(t, tc.want, ClusterLogName(tc.name), "%q", tc.name)
	}

	other := AdhocClusterPrefix + "fedcba9876543210"
	for _, name := range []string{internal, other, AdhocClusterPrefix} {
		id := ClusterLogName(name)
		assert.Regexp(t, `^private-[0-9a-f]{12}$`, id, name)
		assert.False(t, IsAdhocClusterName(id), "%s is no registry name", id)
		assert.Equal(t, id, ClusterLogName(id), "a log id maps to itself")
	}
	assert.NotEqual(t, ClusterLogName(internal), ClusterLogName(other))
}
