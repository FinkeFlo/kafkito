// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package rbac

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

func testPolicy() *Policy {
	return Compile(config.RBACConfig{
		Enabled:     true,
		DefaultRole: "",
		Identity:    config.IdentityConfig{Header: "X-Test-User", AnonymousRole: "viewer"},
		Roles: []config.RoleConfig{
			{
				Name: "admin",
				Permissions: []config.PermissionConfig{
					{Resource: "*", Actions: []string{"*"}},
				},
			},
			{
				Name: "viewer",
				Permissions: []config.PermissionConfig{
					{Resource: "topic:*", Actions: []string{"view", "consume"}},
					{Resource: "group:*", Actions: []string{"view"}},
				},
			},
			{
				Name: "producer",
				Permissions: []config.PermissionConfig{
					{Resource: "topic:orders*", Actions: []string{"view", "produce"}},
				},
			},
		},
		Subjects: []config.SubjectConfig{
			{User: "alice", Roles: []string{"admin"}},
			{User: "bob", Roles: []string{"viewer", "producer"}},
		},
	})
}

func TestAllow_GrantsWildcardAdminAcrossResources(t *testing.T) {
	t.Parallel()

	p := testPolicy()

	cases := []struct {
		name     string
		resource string
		name_    string
		action   string
	}{
		{name: "delete_any_topic", resource: "topic", name_: "anything", action: "delete"},
		{name: "admin_user_resource", resource: "user", name_: "x", action: "admin"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, p.Allow("alice", "c1", tc.resource, tc.name_, tc.action),
				"alice (admin) must be allowed %s on %s/%s", tc.action, tc.resource, tc.name_)
		})
	}
}

func TestAllow_PrefixGlob_HonoursOrdersPrefix(t *testing.T) {
	t.Parallel()

	p := testPolicy()

	t.Run("matches_orders_prefix", func(t *testing.T) {
		t.Parallel()
		assert.True(t, p.Allow("bob", "c1", "topic", "orders-eu", "produce"),
			"bob (producer) must be allowed produce on orders-* topics")
	})

	t.Run("rejects_non_matching_topic", func(t *testing.T) {
		t.Parallel()
		assert.False(t, p.Allow("bob", "c1", "topic", "other", "produce"),
			"bob must NOT be allowed produce on a topic outside orders-* glob")
	})
}

func TestAllow_DeniesActionWithoutMatchingRole(t *testing.T) {
	t.Parallel()

	p := testPolicy()

	t.Run("bob_cannot_delete_topic", func(t *testing.T) {
		t.Parallel()
		assert.False(t, p.Allow("bob", "c1", "topic", "anything", "delete"),
			"bob (viewer+producer) has no delete permission")
	})

	t.Run("unknown_user_without_default_role_has_no_perms", func(t *testing.T) {
		t.Parallel()
		assert.False(t, p.Allow("charlie", "c1", "topic", "x", "view"),
			"unknown user without default_role must be denied")
	})
}

func TestAllow_DisabledPolicy_AlwaysAllows(t *testing.T) {
	t.Parallel()

	p := Compile(config.RBACConfig{Enabled: false})

	assert.True(t, p.Allow("", "c1", "topic", "x", "delete"),
		"disabled RBAC must allow every action regardless of subject")
}

func TestResolveRoles(t *testing.T) {
	t.Parallel()

	p := testPolicy()

	t.Run("anonymous_resolves_to_anonymous_role", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"viewer"}, p.ResolveRoles(""),
			"anonymous user must resolve to configured anonymous_role")
	})

	t.Run("named_subject_resolves_to_assigned_roles", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"admin"}, p.ResolveRoles("alice"))
	})

	t.Run("unknown_subject_without_default_role_resolves_to_empty", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, p.ResolveRoles("charlie"),
			"unknown subject without default_role must resolve to no roles")
	})

	t.Run("unknown_subject_falls_back_to_default_role_when_set", func(t *testing.T) {
		t.Parallel()
		p2 := Compile(config.RBACConfig{Enabled: true, DefaultRole: "viewer"})
		assert.Equal(t, []string{"viewer"}, p2.ResolveRoles("charlie"))
	})
}

func TestMaterializePermissions(t *testing.T) {
	t.Parallel()

	p := testPolicy()

	t.Run("admin_subject_has_wildcard_resource_and_action", func(t *testing.T) {
		t.Parallel()

		perms := p.MaterializePermissions("alice")

		actions, ok := perms["*"]
		require.True(t, ok, "alice must materialise wildcard resource entry, got %v", perms)
		assert.Equal(t, []string{"*"}, actions)
	})

	t.Run("multi_role_subject_unions_per_resource_actions", func(t *testing.T) {
		t.Parallel()

		perms := p.MaterializePermissions("bob")

		topic := perms["topic"]
		sort.Strings(topic)
		want := []string{"consume", "produce", "view"}
		assert.Equal(t, want, topic, "bob's topic actions must union viewer+producer")
	})
}

func TestAllowedResourceNames(t *testing.T) {
	t.Parallel()

	p := testPolicy()

	t.Run("admin_subject_has_unconstrained_access", func(t *testing.T) {
		t.Parallel()

		_, all := p.AllowedResourceNames("alice", "c1", "topic", "view")

		assert.True(t, all, "alice (admin) must report unconstrained access (all=true)")
	})

	t.Run("constrained_subject_returns_glob_list", func(t *testing.T) {
		t.Parallel()

		names, all := p.AllowedResourceNames("bob", "c1", "topic", "produce")

		assert.False(t, all, "bob is constrained to orders-* and must not report all=true")
		assert.Equal(t, []string{"orders*"}, names,
			"bob's produce globs must enumerate orders*")
	})
}

func TestHeader_DefaultsToConfiguredHeaderConstant(t *testing.T) {
	t.Parallel()

	p := Compile(config.RBACConfig{Enabled: true})

	assert.Equal(t, config.DefaultIdentityHeader, p.Header())
}

// TestMatchName pins the exported glob-match contract used by HTTP
// handlers (filterTopicsByRBAC, filterGroupsByRBAC) to filter list
// results. The CVE class is "an attacker name matches a more
// restrictive glob"; the empty-target row pins the intentional
// list-operation wildcard documented on matchResName.
func TestMatchName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		target  string
		want    bool
	}{
		{name: "wildcard_pattern_matches_anything", pattern: "*", target: "topic-anything", want: true},
		{name: "prefix_glob_hit", pattern: "topic-*", target: "topic-orders", want: true},
		{name: "prefix_glob_miss_different_prefix", pattern: "topic-*", target: "metric-orders", want: false},
		{name: "prefix_glob_empty_body_after_prefix_allowed", pattern: "topic-*", target: "topic-", want: true},
		{name: "exact_equality_hit", pattern: "exact", target: "exact", want: true},
		{name: "exact_equality_miss", pattern: "exact", target: "different", want: false},
		{name: "empty_target_intentional_wildcard_for_list_ops", pattern: "topic-*", target: "", want: true},
		{name: "empty_pattern_does_not_wildcard", pattern: "", target: "anything", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := MatchName(tc.pattern, tc.target)

			assert.Equal(t, tc.want, got)
		})
	}
}

// TestEnabled_ReflectsCompiledPolicyConfig pins the accessor contract
// callers depend on to short-circuit the RBAC pipeline. A regression
// that hard-coded true would silently mass-allow; a regression that
// hard-coded false would mass-deny.
func TestEnabled_ReflectsCompiledPolicyConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		configEnabled bool
		want          bool
	}{
		{name: "enabled_true_reflects_true", configEnabled: true, want: true},
		{name: "enabled_false_reflects_false", configEnabled: false, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := Compile(config.RBACConfig{Enabled: tc.configEnabled})

			assert.Equal(t, tc.want, p.Enabled())
		})
	}
}
