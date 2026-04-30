//go:build integration
// +build integration

// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// isSecurityDisabled reports whether the broker rejected an authorization-related
// admin call because no authorizer is configured. Unit-of-test guard for environments
// like confluent-local that ship without an authorizer enabled.
func isSecurityDisabled(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SECURITY_DISABLED") ||
		strings.Contains(s, "authorization is not enabled") ||
		strings.Contains(s, "An authorizer is not configured")
}

// isUnsupported reports whether the error is the broker telling us the API is
// unavailable (e.g. SCRAM on confluent-local).
func isUnsupported(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNSUPPORTED") ||
		strings.Contains(s, "not supported") ||
		strings.Contains(s, "unsupported version")
}

// TestIntegration_ACL_CRUD attempts a Create/List/Delete round-trip. On brokers
// without an authorizer (e.g. confluent-local default) the test gracefully skips
// after asserting the expected SECURITY_DISABLED behaviour.
func TestIntegration_ACL_CRUD(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec := ACLSpec{
		Principal:      "User:test-acl",
		Host:           "*",
		ResourceType:   "TOPIC",
		ResourceName:   "it-acl-topic",
		PatternType:    "LITERAL",
		Operation:      "READ",
		PermissionType: "ALLOW",
	}

	err := reg.CreateACL(ctx, "it", spec)
	if isSecurityDisabled(err) {
		t.Skipf("broker has no authorizer configured: %v", err)
	}
	require.NoError(t, err, "create acl")

	acls, err := reg.ListACLs(ctx, "it")
	require.NoError(t, err)
	found := false
	for _, a := range acls {
		if a.Principal == spec.Principal && a.ResourceName == spec.ResourceName {
			found = true
			break
		}
	}
	require.True(t, found, "created ACL not listed")

	deleted, err := reg.DeleteACL(ctx, "it", spec)
	require.NoError(t, err)
	require.GreaterOrEqual(t, deleted, 1)
}

// TestIntegration_SCRAM_Lifecycle tries to upsert/list/delete a SCRAM credential.
// Skips gracefully if the broker doesn't support SCRAM (e.g. confluent-local).
func TestIntegration_SCRAM_Lifecycle(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	user := "it-scram-user"
	mech := "SCRAM-SHA-256"

	err := reg.UpsertSCRAMUser(ctx, "it", user, mech, "secret-pw", 8192)
	if err != nil && (isUnsupported(err) || isSecurityDisabled(err)) {
		t.Skipf("broker doesn't support SCRAM admin: %v", err)
	}
	require.NoError(t, err, "upsert scram")

	users, err := reg.ListSCRAMUsers(ctx, "it")
	require.NoError(t, err)
	found := false
	for _, u := range users {
		if u.User == user {
			found = true
			break
		}
	}
	require.True(t, found, "created SCRAM user not listed")

	require.NoError(t, reg.DeleteSCRAMUser(ctx, "it", user, mech))
}

// TestIntegration_ResetOffsets produces a few records, commits an offset for a
// dummy group via reset, then asserts the commit took effect via FetchOffsets.
func TestIntegration_ResetOffsets(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := "it-reset-offsets"
	require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{
		Name:              topic,
		Partitions:        1,
		ReplicationFactor: 1,
	}))

	// Produce 5 records so log-end is at 5.
	for i := 0; i < 5; i++ {
		_, err := reg.Produce(ctx, "it", topic, ProduceRequest{Value: "x"})
		require.NoError(t, err)
	}

	group := "it-reset-group"

	// The group coordinator may take a moment to materialize on a fresh broker.
	// Retry the first reset until the broker stops returning COORDINATOR_NOT_AVAILABLE.
	var (
		res     *ResetOffsetsResult
		lastErr error
	)
	for i := 0; i < 10; i++ {
		res, lastErr = reg.ResetOffsets(ctx, "it", group, ResetOffsetsRequest{
			Topic:    topic,
			Strategy: ResetEarliest,
		})
		if lastErr == nil {
			break
		}
		if !strings.Contains(lastErr.Error(), "COORDINATOR_NOT_AVAILABLE") &&
			!strings.Contains(lastErr.Error(), "NOT_COORDINATOR") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, lastErr, "reset earliest")
	require.NotNil(t, res)
	require.Len(t, res.Results, 1)
	require.EqualValues(t, 0, res.Results[0].NewOffset)

	// Reset to latest (=5).
	res, err := reg.ResetOffsets(ctx, "it", group, ResetOffsetsRequest{
		Topic:    topic,
		Strategy: ResetLatest,
	})
	require.NoError(t, err, "reset latest")
	require.EqualValues(t, 5, res.Results[0].NewOffset)

	// Reset to specific offset (=2).
	res, err = reg.ResetOffsets(ctx, "it", group, ResetOffsetsRequest{
		Topic:    topic,
		Strategy: ResetToOffset,
		Offset:   2,
	})
	require.NoError(t, err, "reset to specific")
	require.EqualValues(t, 2, res.Results[0].NewOffset)
}

// TestIntegration_SearchMessages produces JSON records and exercises the
// contains and jsonpath search modes.
func TestIntegration_SearchMessages(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := "it-search"
	require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{
		Name:              topic,
		Partitions:        1,
		ReplicationFactor: 1,
	}))

	payloads := []string{
		`{"id":1,"amount":50,"status":"new"}`,
		`{"id":2,"amount":250,"status":"shipped"}`,
		`{"id":3,"amount":1500,"status":"shipped"}`,
		`{"id":4,"amount":900,"status":"cancelled"}`,
		`{"id":5,"amount":2000,"status":"shipped"}`,
	}
	for _, p := range payloads {
		_, err := reg.Produce(ctx, "it", topic, ProduceRequest{Value: p})
		require.NoError(t, err)
	}

	// 1) contains "shipped" in value zone → 3 hits (id 2, 3, 5).
	containsRes, err := reg.SearchMessages(ctx, "it", topic, SearchOptions{
		Partition:   -1,
		Limit:       100,
		Budget:      1000,
		Direction:   DirOldestFirst,
		Mode:        SearchModeContains,
		Value:       "shipped",
		Zones:       []SearchZone{ZoneValue},
		StopOnLimit: true,
		Timeout:     8 * time.Second,
	})
	require.NoError(t, err, "contains search")
	require.Equal(t, 3, containsRes.Stats.Matched, "contains 'shipped' should match 3 records")

	// 2) jsonpath $.amount > 1000 → 2 hits (id 3, 5).
	jpRes, err := reg.SearchMessages(ctx, "it", topic, SearchOptions{
		Partition:   -1,
		Limit:       100,
		Budget:      1000,
		Direction:   DirOldestFirst,
		Mode:        SearchModeJSONPath,
		Path:        "$.amount",
		Op:          OpGt,
		Value:       "1000",
		Zones:       []SearchZone{ZoneValue},
		StopOnLimit: true,
		Timeout:     8 * time.Second,
	})
	require.NoError(t, err, "jsonpath search")
	require.Equal(t, 2, jpRes.Stats.Matched, "jsonpath $.amount > 1000 should match 2 records")
}
