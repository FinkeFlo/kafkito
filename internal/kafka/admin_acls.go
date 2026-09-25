// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// --- List ACLs ---------------------------------------------------------------

// ACLEntry is a flattened described ACL.
type ACLEntry struct {
	Principal      string `json:"principal"`
	Host           string `json:"host"`
	ResourceType   string `json:"resource_type"`
	ResourceName   string `json:"resource_name"`
	PatternType    string `json:"pattern_type"`
	Operation      string `json:"operation"`
	PermissionType string `json:"permission_type"`
}

// ListACLs describes every ACL visible to the current principal.
func (r *Registry) ListACLs(ctx context.Context, cluster string) ([]ACLEntry, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	b := kadm.NewACLs().
		AnyResource().
		ResourcePatternType(kadm.ACLPatternAny).
		Operations().
		Allow().AllowHosts().
		Deny().DenyHosts()
	results, err := adm.DescribeACLs(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("describe acls: %w", err)
	}
	out := make([]ACLEntry, 0, 64)
	for _, res := range results {
		if res.Err != nil {
			return nil, fmt.Errorf("describe acls filter: %s: %w", res.ErrMessage, res.Err)
		}
		for _, a := range res.Described {
			out = append(out, ACLEntry{
				Principal:      a.Principal,
				Host:           a.Host,
				ResourceType:   a.Type.String(),
				ResourceName:   a.Name,
				PatternType:    a.Pattern.String(),
				Operation:      a.Operation.String(),
				PermissionType: a.Permission.String(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ResourceType != out[j].ResourceType {
			return out[i].ResourceType < out[j].ResourceType
		}
		if out[i].ResourceName != out[j].ResourceName {
			return out[i].ResourceName < out[j].ResourceName
		}
		if out[i].Principal != out[j].Principal {
			return out[i].Principal < out[j].Principal
		}
		return out[i].Operation < out[j].Operation
	})
	_ = errors.New // keep errors import if unused
	return out, nil
}

// --- Create / Delete ACL -----------------------------------------------------

// ACLSpec describes a single ACL to create or delete (exact-match filter).
//
// All fields are required; resource_type + pattern_type + operation +
// permission_type use the canonical Kafka names (TOPIC, GROUP, CLUSTER,
// TRANSACTIONAL_ID, DELEGATION_TOKEN, USER / LITERAL, PREFIXED / READ,
// WRITE, CREATE, DELETE, ALTER, DESCRIBE, CLUSTER_ACTION, DESCRIBE_CONFIGS,
// ALTER_CONFIGS, IDEMPOTENT_WRITE, ALL / ALLOW, DENY).
type ACLSpec struct {
	Principal      string `json:"principal"`
	Host           string `json:"host"`
	ResourceType   string `json:"resource_type"`
	ResourceName   string `json:"resource_name"`
	PatternType    string `json:"pattern_type"`
	Operation      string `json:"operation"`
	PermissionType string `json:"permission_type"`
}

func (s ACLSpec) applyToBuilder(b *kadm.ACLBuilder) error {
	if strings.TrimSpace(s.Principal) == "" {
		return errors.New("principal is required")
	}
	if strings.TrimSpace(s.ResourceName) == "" {
		return errors.New("resource_name is required")
	}
	host := s.Host
	if strings.TrimSpace(host) == "" {
		host = "*"
	}
	rt, err := kmsg.ParseACLResourceType(s.ResourceType)
	if err != nil {
		return fmt.Errorf("resource_type: %w", err)
	}
	pt, err := kmsg.ParseACLResourcePatternType(s.PatternType)
	if err != nil {
		return fmt.Errorf("pattern_type: %w", err)
	}
	op, err := kmsg.ParseACLOperation(s.Operation)
	if err != nil {
		return fmt.Errorf("operation: %w", err)
	}
	perm, err := kmsg.ParseACLPermissionType(s.PermissionType)
	if err != nil {
		return fmt.Errorf("permission_type: %w", err)
	}

	switch rt {
	case kmsg.ACLResourceTypeTopic:
		b.Topics(s.ResourceName)
	case kmsg.ACLResourceTypeGroup:
		b.Groups(s.ResourceName)
	case kmsg.ACLResourceTypeCluster:
		b.Clusters()
	case kmsg.ACLResourceTypeTransactionalId:
		b.TransactionalIDs(s.ResourceName)
	case kmsg.ACLResourceTypeDelegationToken:
		b.DelegationTokens(s.ResourceName)
	default:
		return fmt.Errorf("resource_type %q not supported", s.ResourceType)
	}
	b.ResourcePatternType(pt).Operations(op)

	switch perm {
	case kmsg.ACLPermissionTypeAllow:
		b.Allow(s.Principal).AllowHosts(host)
	case kmsg.ACLPermissionTypeDeny:
		b.Deny(s.Principal).DenyHosts(host)
	default:
		return fmt.Errorf("permission_type %q must be ALLOW or DENY", s.PermissionType)
	}
	return nil
}

// CreateACL creates a single ACL on the cluster.
func (r *Registry) CreateACL(ctx context.Context, cluster string, spec ACLSpec) error {
	adm, err := r.Admin(cluster)
	if err != nil {
		return err
	}
	b := kadm.NewACLs()
	if err := spec.applyToBuilder(b); err != nil {
		return err
	}
	if err := b.ValidateCreate(); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	res, err := adm.CreateACLs(ctx, b)
	if err != nil {
		return fmt.Errorf("create acl %s %s %s on %s:%s: %w",
			spec.PermissionType, spec.Operation, spec.Principal,
			spec.ResourceType, spec.ResourceName, err)
	}
	for _, it := range res {
		if it.Err != nil {
			return fmt.Errorf("create acl %s on %s:%s: %s: %w",
				spec.Operation, spec.ResourceType, spec.ResourceName,
				it.ErrMessage, it.Err)
		}
	}
	return nil
}

// DeleteACL deletes a single ACL on the cluster matching the exact filter.
func (r *Registry) DeleteACL(ctx context.Context, cluster string, spec ACLSpec) (int, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return 0, err
	}
	b := kadm.NewACLs()
	if err := spec.applyToBuilder(b); err != nil {
		return 0, err
	}
	if err := b.ValidateDelete(); err != nil {
		return 0, fmt.Errorf("validate: %w", err)
	}
	res, err := adm.DeleteACLs(ctx, b)
	if err != nil {
		return 0, fmt.Errorf("delete acl on %s:%s: %w", spec.ResourceType, spec.ResourceName, err)
	}
	deleted := 0
	for _, it := range res {
		if it.Err != nil {
			return deleted, fmt.Errorf("delete acl on %s:%s: %s: %w",
				spec.ResourceType, spec.ResourceName, it.ErrMessage, it.Err)
		}
		for _, m := range it.Deleted {
			if m.Err != nil {
				return deleted, fmt.Errorf("delete acl match %s:%s: %s: %w",
					spec.ResourceType, spec.ResourceName, m.ErrMessage, m.Err)
			}
			deleted++
		}
	}
	return deleted, nil
}
