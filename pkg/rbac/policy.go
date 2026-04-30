// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

// Package rbac implements the YAML-driven role-based access control policy
// engine used by kafkito. It turns a config.RBACConfig into an evaluatable
// Policy that the HTTP middleware consults for every cluster-scoped request.
package rbac

import (
	"strings"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

// Canonical RBAC actions.
const (
	ActionView    = "view"
	ActionConsume = "consume"
	ActionProduce = "produce"
	ActionEdit    = "edit"
	ActionDelete  = "delete"
	ActionAdmin   = "admin"
)

// AllActions is the full set of canonical actions.
var AllActions = []string{ActionView, ActionConsume, ActionProduce, ActionEdit, ActionDelete, ActionAdmin}

// Policy is a compiled RBAC policy ready for evaluation.
type Policy struct {
	enabled     bool
	header      string
	anonRole    string
	defaultRole string
	rolePerms   map[string][]compiledPerm
	userRoles   map[string][]string
}

type compiledPerm struct {
	resType    string
	resName    string
	actions    map[string]bool
	allActions bool
}

// Compile builds an evaluatable Policy from config.
func Compile(cfg config.RBACConfig) *Policy {
	p := &Policy{
		enabled:     cfg.Enabled,
		header:      cfg.Identity.Header,
		anonRole:    cfg.Identity.AnonymousRole,
		defaultRole: cfg.DefaultRole,
		rolePerms:   make(map[string][]compiledPerm),
		userRoles:   make(map[string][]string),
	}
	if p.header == "" {
		p.header = config.DefaultIdentityHeader
	}

	for _, role := range cfg.Roles {
		var perms []compiledPerm
		for _, pr := range role.Permissions {
			cp := compiledPerm{actions: make(map[string]bool)}
			if pr.Resource == "*" {
				cp.resType = "*"
				cp.resName = "*"
			} else {
				parts := strings.SplitN(pr.Resource, ":", 2)
				if len(parts) == 2 {
					cp.resType = parts[0]
					cp.resName = parts[1]
				} else {
					cp.resType = parts[0]
					cp.resName = "*"
				}
			}
			for _, a := range pr.Actions {
				if a == "*" {
					cp.allActions = true
					break
				}
				cp.actions[a] = true
			}
			perms = append(perms, cp)
		}
		p.rolePerms[role.Name] = perms
	}

	for _, subj := range cfg.Subjects {
		p.userRoles[subj.User] = subj.Roles
	}
	return p
}

// Enabled returns whether RBAC enforcement is active.
func (p *Policy) Enabled() bool { return p.enabled }

// Header returns the HTTP header name used to resolve the identity.
func (p *Policy) Header() string { return p.header }

// ResolveRoles returns the roles for a given user identity.
// Empty user means anonymous.
func (p *Policy) ResolveRoles(user string) []string {
	if user == "" {
		if p.anonRole != "" {
			return []string{p.anonRole}
		}
		if p.defaultRole != "" {
			return []string{p.defaultRole}
		}
		return nil
	}
	if roles, ok := p.userRoles[user]; ok {
		return roles
	}
	if p.defaultRole != "" {
		return []string{p.defaultRole}
	}
	return nil
}

// Allow returns true if the subject is allowed action on resource type:name
// within cluster. resName may be empty (for list operations).
func (p *Policy) Allow(user, _cluster, resType, resName, action string) bool {
	if !p.enabled {
		return true
	}
	roles := p.ResolveRoles(user)
	for _, role := range roles {
		for _, perm := range p.rolePerms[role] {
			if !matchResType(perm.resType, resType) {
				continue
			}
			if !matchResName(perm.resName, resName) {
				continue
			}
			if perm.allActions || perm.actions[action] {
				return true
			}
		}
	}
	return false
}

// AllowedResourceNames returns the set of resource name globs (within a type)
// the user has the given action on. Returns (nil, true) if all are allowed,
// (names, false) for a specific set. Used for filtering list results.
func (p *Policy) AllowedResourceNames(user, _cluster, resType, action string) (names []string, all bool) {
	if !p.enabled {
		return nil, true
	}
	roles := p.ResolveRoles(user)
	for _, role := range roles {
		for _, perm := range p.rolePerms[role] {
			if !matchResType(perm.resType, resType) {
				continue
			}
			if !perm.allActions && !perm.actions[action] {
				continue
			}
			if perm.resName == "*" {
				return nil, true
			}
		}
	}
	seen := map[string]bool{}
	for _, role := range roles {
		for _, perm := range p.rolePerms[role] {
			if !matchResType(perm.resType, resType) {
				continue
			}
			if !perm.allActions && !perm.actions[action] {
				continue
			}
			if !seen[perm.resName] {
				seen[perm.resName] = true
				names = append(names, perm.resName)
			}
		}
	}
	return names, false
}

// MaterializePermissions returns a flat map of resourceType -> []actions for
// the user. Used by the /api/v1/me endpoint.
func (p *Policy) MaterializePermissions(user string) map[string][]string {
	result := make(map[string][]string)
	if !p.enabled {
		for _, rt := range []string{"cluster", "topic", "group", "schema", "acl", "user"} {
			result[rt] = AllActions
		}
		return result
	}
	roles := p.ResolveRoles(user)
	actionSets := make(map[string]map[string]bool)
	for _, role := range roles {
		for _, perm := range p.rolePerms[role] {
			rt := perm.resType
			if _, ok := actionSets[rt]; !ok {
				actionSets[rt] = make(map[string]bool)
			}
			if perm.allActions {
				actionSets[rt]["*"] = true
			} else {
				for a := range perm.actions {
					actionSets[rt][a] = true
				}
			}
		}
	}
	for rt, acts := range actionSets {
		if acts["*"] {
			result[rt] = []string{"*"}
			continue
		}
		var list []string
		for a := range acts {
			list = append(list, a)
		}
		result[rt] = list
	}
	return result
}

func matchResType(pattern, target string) bool {
	return pattern == "*" || pattern == target
}

// matchResName matches a resource name against a pattern. Pattern may be "*"
// (any), "prefix*" (prefix glob), or an exact string. An empty target matches
// any pattern (used for list operations).
func matchResName(pattern, target string) bool {
	if pattern == "*" {
		return true
	}
	if target == "" || target == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(target, prefix)
	}
	return pattern == target
}

// MatchName reports whether a resource name matches a glob pattern from the
// policy. It is exported so HTTP handlers can filter list results using the
// globs returned by AllowedResourceNames.
func MatchName(pattern, name string) bool {
	return matchResName(pattern, name)
}
