// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package rbac

import (
	"sort"
	"testing"

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

func TestAllowWildcardResourceAndAction(t *testing.T) {
	p := testPolicy()
	if !p.Allow("alice", "c1", "topic", "anything", "delete") {
		t.Errorf("alice should be allowed delete on any topic")
	}
	if !p.Allow("alice", "c1", "user", "x", "admin") {
		t.Errorf("alice should be allowed admin on user")
	}
}

func TestAllowPrefixGlob(t *testing.T) {
	p := testPolicy()
	if !p.Allow("bob", "c1", "topic", "orders-eu", "produce") {
		t.Errorf("bob should be allowed produce on orders-eu")
	}
	if p.Allow("bob", "c1", "topic", "other", "produce") {
		t.Errorf("bob should NOT be allowed produce on 'other'")
	}
}

func TestDenyNoMatchingRole(t *testing.T) {
	p := testPolicy()
	if p.Allow("bob", "c1", "topic", "anything", "delete") {
		t.Errorf("bob should not be allowed delete")
	}
	if p.Allow("charlie", "c1", "topic", "x", "view") {
		t.Errorf("unknown user without default_role should have no permissions")
	}
}

func TestDisabledAlwaysAllow(t *testing.T) {
	p := Compile(config.RBACConfig{Enabled: false})
	if !p.Allow("", "c1", "topic", "x", "delete") {
		t.Errorf("disabled RBAC must allow everything")
	}
}

func TestResolveRolesAnonymousAndDefault(t *testing.T) {
	p := testPolicy()
	if got := p.ResolveRoles(""); len(got) != 1 || got[0] != "viewer" {
		t.Errorf("anonymous should resolve to [viewer], got %v", got)
	}
	if got := p.ResolveRoles("alice"); len(got) != 1 || got[0] != "admin" {
		t.Errorf("alice should resolve to [admin], got %v", got)
	}
	if got := p.ResolveRoles("charlie"); len(got) != 0 {
		t.Errorf("charlie has no role + no default, got %v", got)
	}

	p2 := Compile(config.RBACConfig{Enabled: true, DefaultRole: "viewer"})
	if got := p2.ResolveRoles("charlie"); len(got) != 1 || got[0] != "viewer" {
		t.Errorf("default_role should apply, got %v", got)
	}
}

func TestMaterializePermissions(t *testing.T) {
	p := testPolicy()
	perms := p.MaterializePermissions("alice")
	if v, ok := perms["*"]; !ok || len(v) != 1 || v[0] != "*" {
		t.Errorf("alice should have *:*, got %v", perms)
	}

	perms = p.MaterializePermissions("bob")
	topic := perms["topic"]
	sort.Strings(topic)
	want := map[string]bool{"view": true, "consume": true, "produce": true}
	for _, a := range topic {
		if !want[a] {
			t.Errorf("unexpected action %q for bob on topic", a)
		}
	}
}

func TestAllowedResourceNames(t *testing.T) {
	p := testPolicy()
	names, all := p.AllowedResourceNames("alice", "c1", "topic", "view")
	if !all {
		t.Errorf("alice should have all topics for view, got %v all=%v", names, all)
	}
	names, all = p.AllowedResourceNames("bob", "c1", "topic", "produce")
	if all {
		t.Errorf("bob should NOT have all topics for produce")
	}
	if len(names) != 1 || names[0] != "orders*" {
		t.Errorf("bob produce globs = %v, want [orders*]", names)
	}
}

func TestHeaderDefault(t *testing.T) {
	p := Compile(config.RBACConfig{Enabled: true})
	if p.Header() != config.DefaultIdentityHeader {
		t.Errorf("header default = %q", p.Header())
	}
}
