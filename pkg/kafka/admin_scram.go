// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/twmb/franz-go/pkg/kadm"
)

// --- SCRAM user management ---------------------------------------------------

// SCRAMCredential describes a single SCRAM mechanism for a user.
type SCRAMCredential struct {
	Mechanism  string `json:"mechanism"`
	Iterations int32  `json:"iterations"`
}

// SCRAMUser aggregates all SCRAM credentials for a user.
type SCRAMUser struct {
	User        string            `json:"user"`
	Credentials []SCRAMCredential `json:"credentials"`
}

func parseSCRAMMechanism(s string) (kadm.ScramMechanism, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SCRAM-SHA-256", "SHA-256", "SHA256":
		return kadm.ScramSha256, nil
	case "SCRAM-SHA-512", "SHA-512", "SHA512":
		return kadm.ScramSha512, nil
	default:
		return 0, fmt.Errorf("unknown SCRAM mechanism %q (want SCRAM-SHA-256 or SCRAM-SHA-512)", s)
	}
}

// ListSCRAMUsers returns all users that have SCRAM credentials.
func (r *Registry) ListSCRAMUsers(ctx context.Context, cluster string) ([]SCRAMUser, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	described, err := adm.DescribeUserSCRAMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe scram users: %w", err)
	}
	out := make([]SCRAMUser, 0, len(described))
	for _, u := range described.Sorted() {
		if u.Err != nil {
			continue
		}
		creds := make([]SCRAMCredential, 0, len(u.CredInfos))
		for _, ci := range u.CredInfos {
			creds = append(creds, SCRAMCredential{
				Mechanism:  ci.Mechanism.String(),
				Iterations: ci.Iterations,
			})
		}
		out = append(out, SCRAMUser{User: u.User, Credentials: creds})
	}
	return out, nil
}

// UpsertSCRAMUser creates or updates a SCRAM credential for the user.
//
// Iterations must be between 4096 and 16384. If zero we default to 8192.
func (r *Registry) UpsertSCRAMUser(ctx context.Context, cluster, user, mechanism, password string, iterations int32) error {
	adm, err := r.Admin(cluster)
	if err != nil {
		return err
	}
	if strings.TrimSpace(user) == "" {
		return errors.New("user is required")
	}
	if password == "" {
		return errors.New("password is required")
	}
	mech, err := parseSCRAMMechanism(mechanism)
	if err != nil {
		return err
	}
	if iterations == 0 {
		iterations = 8192
	}
	if iterations < 4096 || iterations > 16384 {
		return fmt.Errorf("iterations %d out of range [4096,16384]", iterations)
	}
	res, err := adm.AlterUserSCRAMs(ctx, nil, []kadm.UpsertSCRAM{{
		User:       user,
		Mechanism:  mech,
		Iterations: iterations,
		Password:   password,
	}})
	if err != nil {
		return fmt.Errorf("upsert scram credential for user %q (%s): %w", user, mech, err)
	}
	for _, r := range res {
		if r.Err != nil {
			return fmt.Errorf("upsert scram %s: %s: %w", r.User, r.ErrMessage, r.Err)
		}
	}
	return nil
}

// DeleteSCRAMUser removes a specific SCRAM mechanism credential for the user.
func (r *Registry) DeleteSCRAMUser(ctx context.Context, cluster, user, mechanism string) error {
	adm, err := r.Admin(cluster)
	if err != nil {
		return err
	}
	if strings.TrimSpace(user) == "" {
		return errors.New("user is required")
	}
	mech, err := parseSCRAMMechanism(mechanism)
	if err != nil {
		return err
	}
	res, err := adm.AlterUserSCRAMs(ctx, []kadm.DeleteSCRAM{{
		User:      user,
		Mechanism: mech,
	}}, nil)
	if err != nil {
		return fmt.Errorf("delete scram credential for user %q (%s): %w", user, mech, err)
	}
	for _, r := range res {
		if r.Err != nil {
			return fmt.Errorf("delete scram %s: %s: %w", r.User, r.ErrMessage, r.Err)
		}
	}
	return nil
}
