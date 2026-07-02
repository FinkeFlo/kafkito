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
)

// --- Reset consumer group offsets ---------------------------------------------

// ResetOffsetStrategy selects how new offsets are computed.
//   - earliest: commit the current log-start offset for each selected partition.
//   - latest:   commit the current log-end offset for each selected partition.
//   - offset:   commit a specific absolute offset (per partition or global).
//   - timestamp: resolve the first offset at/after TimestampMs per partition.
//   - shift-by: committed = current-committed + Shift (can be negative).
type ResetOffsetStrategy string

// Reset strategies supported by ResetOffsets.
const (
	ResetEarliest  ResetOffsetStrategy = "earliest"
	ResetLatest    ResetOffsetStrategy = "latest"
	ResetToOffset  ResetOffsetStrategy = "offset"
	ResetTimestamp ResetOffsetStrategy = "timestamp"
	ResetShiftBy   ResetOffsetStrategy = "shift-by"
)

// ResetOffsetsRequest describes a reset for one topic on a single group.
type ResetOffsetsRequest struct {
	Topic       string              `json:"topic"`
	Partitions  []int32             `json:"partitions,omitempty"` // omit / empty = all partitions of Topic
	Strategy    ResetOffsetStrategy `json:"strategy"`
	Offset      int64               `json:"offset,omitempty"`       // when strategy=offset
	TimestampMs int64               `json:"timestamp_ms,omitempty"` // when strategy=timestamp
	Shift       int64               `json:"shift,omitempty"`        // when strategy=shift-by
	DryRun      bool                `json:"dry_run,omitempty"`
}

// ResetOffsetResult is the per-partition outcome of a reset.
type ResetOffsetResult struct {
	Partition int32  `json:"partition"`
	OldOffset int64  `json:"old_offset"` // -1 if no prior commit
	NewOffset int64  `json:"new_offset"`
	EndOffset int64  `json:"end_offset"` // log-end offset, -1 if unknown
	Error     string `json:"error,omitempty"`
}

// ResetOffsetsResult bundles the outcomes.
type ResetOffsetsResult struct {
	Group   string              `json:"group"`
	Topic   string              `json:"topic"`
	DryRun  bool                `json:"dry_run"`
	Results []ResetOffsetResult `json:"results"`
}

// ResetOffsets computes and optionally commits new offsets for a consumer group.
// The group must be Empty or Dead; resetting offsets of an active group is
// refused by the broker (GROUP_IS_NOT_EMPTY). We still pass the request through
// so the broker-side error bubbles up cleanly.
func (r *Registry) ResetOffsets(ctx context.Context, cluster, group string, req ResetOffsetsRequest) (*ResetOffsetsResult, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	return r.resolveAndCommit(ctx, adm, group, req)
}

// resolveAndCommit is the shared core behind ResetOffsets and CreateGroup: it
// selects target partitions, resolves the new offsets per strategy (with
// clamping), and commits them (unless DryRun) for the given group. The caller
// supplies an already-acquired admin client and is responsible for any
// group-state preconditions.
func (r *Registry) resolveAndCommit(ctx context.Context, adm *kadm.Client, group string, req ResetOffsetsRequest) (*ResetOffsetsResult, error) {
	if strings.TrimSpace(req.Topic) == "" {
		return nil, errors.New("reset offsets: topic required")
	}
	switch req.Strategy {
	case ResetEarliest, ResetLatest, ResetToOffset, ResetTimestamp, ResetShiftBy:
	default:
		return nil, fmt.Errorf("reset offsets: unknown strategy %q", req.Strategy)
	}

	// 1) Determine the target partitions — either user-supplied or "all partitions of topic".
	var targets []int32
	if len(req.Partitions) > 0 {
		seen := map[int32]struct{}{}
		for _, p := range req.Partitions {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			targets = append(targets, p)
		}
	} else {
		td, err := adm.ListTopics(ctx, req.Topic)
		if err != nil {
			return nil, fmt.Errorf("list topic %q: %w", req.Topic, err)
		}
		t, ok := td[req.Topic]
		if !ok || t.Err != nil {
			if ok && t.Err != nil {
				return nil, fmt.Errorf("describe topic %q: %w", req.Topic, t.Err)
			}
			return nil, fmt.Errorf("topic %q not found", req.Topic)
		}
		for p := range t.Partitions {
			targets = append(targets, p)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })

	// 2) Resolve new offsets per partition.
	var newOffsets kadm.ListedOffsets
	var err error
	switch req.Strategy {
	case ResetEarliest:
		newOffsets, err = adm.ListStartOffsets(ctx, req.Topic)
	case ResetLatest:
		newOffsets, err = adm.ListEndOffsets(ctx, req.Topic)
	case ResetTimestamp:
		if req.TimestampMs <= 0 {
			return nil, errors.New("reset offsets: timestamp_ms required for timestamp strategy")
		}
		newOffsets, err = adm.ListOffsetsAfterMilli(ctx, req.TimestampMs, req.Topic)
	}
	if err != nil {
		return nil, fmt.Errorf("list offsets for topic %q: %w", req.Topic, err)
	}

	// 3) Fetch current committed offsets for the group (for OldOffset + shift-by).
	committed, cerr := adm.FetchOffsetsForTopics(ctx, group, req.Topic)
	if cerr != nil {
		return nil, fmt.Errorf("fetch committed offsets for group %q topic %q: %w", group, req.Topic, cerr)
	}

	// 4) Also fetch the log-end offsets so we can clamp user-provided absolute offsets
	//    to the valid range [start, end].
	ends, endsErr := adm.ListEndOffsets(ctx, req.Topic)
	starts, startsErr := adm.ListStartOffsets(ctx, req.Topic)
	boundsErr := endsErr != nil || startsErr != nil

	toCommit := kadm.Offsets{}
	results := make([]ResetOffsetResult, 0, len(targets))
	for _, p := range targets {
		res := ResetOffsetResult{Partition: p, OldOffset: -1, NewOffset: -1, EndOffset: -1}

		if c, ok := committed.Lookup(req.Topic, p); ok && c.Err == nil {
			res.OldOffset = c.At
		}
		if e, ok := ends.Lookup(req.Topic, p); ok && e.Err == nil {
			res.EndOffset = e.Offset
		}

		var newAt int64 = -1
		switch req.Strategy {
		case ResetEarliest, ResetLatest, ResetTimestamp:
			if lo, ok := newOffsets.Lookup(req.Topic, p); ok {
				if lo.Err != nil {
					res.Error = lo.Err.Error()
				} else {
					newAt = lo.Offset
				}
			} else {
				res.Error = "partition offset not resolved"
			}
		case ResetToOffset:
			if boundsErr {
				res.Error = "offset bounds unavailable; refusing to commit an unclamped absolute offset"
			} else {
				newAt = clampToBounds(req.Offset, starts, ends, req.Topic, p)
			}
		case ResetShiftBy:
			if res.OldOffset < 0 {
				res.Error = "no prior commit to shift from"
			} else if boundsErr {
				res.Error = "offset bounds unavailable; refusing to commit an unclamped shifted offset"
			} else {
				newAt = clampToBounds(res.OldOffset+req.Shift, starts, ends, req.Topic, p)
			}
		}

		res.NewOffset = newAt
		if res.Error == "" && newAt >= 0 {
			toCommit.AddOffset(req.Topic, p, newAt, -1)
		}
		results = append(results, res)
	}

	out := &ResetOffsetsResult{
		Group:   group,
		Topic:   req.Topic,
		DryRun:  req.DryRun,
		Results: results,
	}

	if req.DryRun || len(toCommit) == 0 {
		return out, nil
	}

	commitResp, cerr := adm.CommitOffsets(ctx, group, toCommit)
	if cerr != nil {
		return nil, fmt.Errorf("commit offsets for group %q topic %q: %w", group, req.Topic, cerr)
	}
	// Merge per-partition commit errors back into the result.
	for i, r := range out.Results {
		if r.Error != "" || r.NewOffset < 0 {
			continue
		}
		if parts, ok := commitResp[req.Topic]; ok {
			if ore, ok := parts[r.Partition]; ok && ore.Err != nil {
				out.Results[i].Error = ore.Err.Error()
			}
		}
	}
	return out, nil
}

// clampToBounds clamps off into [start, end] for topic/partition p, using only
// bound entries that have no per-partition error. A missing or errored bound on
// one side leaves that side unconstrained.
func clampToBounds(off int64, starts, ends kadm.ListedOffsets, topic string, p int32) int64 {
	if s, ok := starts.Lookup(topic, p); ok && s.Err == nil && off < s.Offset {
		off = s.Offset
	}
	if e, ok := ends.Lookup(topic, p); ok && e.Err == nil && off > e.Offset {
		off = e.Offset
	}
	return off
}

// --- Create consumer group ----------------------------------------------------

// ErrGroupExists is returned by CreateGroup when the target group already
// exists (has active members or committed offsets). Callers map this to 409.
var ErrGroupExists = errors.New("consumer group already exists")

// CreateGroupRequest describes a new consumer group to bind to a single topic.
// The group is created by committing initial offsets for every partition of
// Topic under the chosen strategy. shift-by is not accepted: a brand-new group
// has no prior commit to shift from.
type CreateGroupRequest struct {
	GroupID     string              `json:"group_id"`
	Topic       string              `json:"topic"`
	Strategy    ResetOffsetStrategy `json:"strategy"`               // earliest | latest | timestamp | offset
	Offset      int64               `json:"offset,omitempty"`       // when strategy=offset
	TimestampMs int64               `json:"timestamp_ms,omitempty"` // when strategy=timestamp
	DryRun      bool                `json:"dry_run,omitempty"`
}

// CreateGroup creates a new consumer group bound to a single topic by committing
// initial offsets for all its partitions. It refuses to touch a group that
// already exists (ErrGroupExists) and rejects the shift-by strategy. The
// offset-resolution and commit logic is shared with ResetOffsets via
// resolveAndCommit.
func (r *Registry) CreateGroup(ctx context.Context, cluster string, req CreateGroupRequest) (*ResetOffsetsResult, error) {
	group := strings.TrimSpace(req.GroupID)
	if group == "" {
		return nil, errors.New("create group: group_id required")
	}
	if strings.TrimSpace(req.Topic) == "" {
		return nil, errors.New("create group: topic required")
	}
	switch req.Strategy {
	case ResetEarliest, ResetLatest, ResetToOffset, ResetTimestamp:
	case ResetShiftBy:
		return nil, errors.New("create group: shift-by is not supported when creating a group")
	default:
		return nil, fmt.Errorf("create group: unknown strategy %q", req.Strategy)
	}

	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}

	// Precondition: the group must not already exist. A group is "existing" if
	// ListGroups reports it (active members or committed offsets).
	listed, err := adm.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups on cluster %q: %w", cluster, err)
	}
	for _, name := range listed.Groups() {
		if name == group {
			return nil, ErrGroupExists
		}
	}

	return r.resolveAndCommit(ctx, adm, group, ResetOffsetsRequest{
		Topic:       req.Topic,
		Strategy:    req.Strategy,
		Offset:      req.Offset,
		TimestampMs: req.TimestampMs,
		DryRun:      req.DryRun,
	})
}

// DeleteGroup removes an empty/dead consumer group.
func (r *Registry) DeleteGroup(ctx context.Context, cluster, group string) error {
	adm, err := r.Admin(cluster)
	if err != nil {
		return err
	}
	resp, err := adm.DeleteGroup(ctx, group)
	if err != nil {
		return fmt.Errorf("delete group %q: %w", group, err)
	}
	if resp.Err != nil {
		return fmt.Errorf("delete group %q: %w", group, resp.Err)
	}
	return nil
}
