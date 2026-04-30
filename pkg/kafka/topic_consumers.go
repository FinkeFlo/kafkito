// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/twmb/franz-go/pkg/kadm"
	"golang.org/x/sync/errgroup"
)

// TopicConsumer describes a consumer group that reads from a topic.
//
// PartitionsAssigned lists, in ascending order, the partitions of the target
// topic currently assigned to any member of the group. Lag is the sum of
// committed-offset lag across those partitions; LagKnown is false if any
// partition's log-end offset could not be resolved.
type TopicConsumer struct {
	GroupID            string  `json:"group_id"`
	State              string  `json:"state"`
	Members            int     `json:"members"`
	PartitionsAssigned []int32 `json:"partitions_assigned"`
	Lag                int64   `json:"lag"`
	LagKnown           bool    `json:"lag_known"`
	Error              string  `json:"error,omitempty"`
}

// ErrTopicNotFound signals that the requested topic is not present on the
// cluster (as opposed to having no consumers).
var ErrTopicNotFound = errors.New("topic not found")

// topicConsumerInput is a kadm-free, test-friendly view of one candidate group.
type topicConsumerInput struct {
	GroupID            string
	State              string
	DescribeErr        string // already classified; empty if none
	AssigningMembers   int
	AssignedPartitions []int32 // sorted ascending, partitions of target topic
}

// offsetFetcher fetches committed offsets of one group for the target topic,
// keyed by partition. A non-nil error is recorded as the group's Error.
type offsetFetcher func(ctx context.Context, group string) (map[int32]int64, error)

// ListTopicConsumers returns the consumer groups currently reading from the
// given topic on the named cluster.
//
// Implementation notes:
//   - Dead groups are skipped.
//   - Per-group offset fetches run in parallel under an errgroup with a
//     concurrency cap of 8 (backend-styleguide §5).
//   - ListEndOffsets is called once per request and the result is reused for
//     lag computation.
//   - Partial errors (e.g. one group cannot be described or its offsets
//     fetched) are surfaced via TopicConsumer.Error; the call only returns an
//     error for cluster-level failures.
//
// TODO(perf): Kafka has no native reverse-lookup from topic to groups. For
// clusters with thousands of groups this scales O(n) per request; consider an
// async background poller in Registry that maintains a topic->groups index
// (TTL'd, per-cluster) and serves reads from cache.
func (r *Registry) ListTopicConsumers(ctx context.Context, cluster, topic string) ([]TopicConsumer, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}

	md, err := adm.Metadata(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("metadata for topic %q on cluster %q: %w", topic, cluster, err)
	}
	t, ok := md.Topics[topic]
	if !ok || t.Err != nil {
		return nil, fmt.Errorf("%w: %s", ErrTopicNotFound, topic)
	}

	listed, err := adm.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups for topic %q on cluster %q: %w", topic, cluster, err)
	}
	names := listed.Groups()
	if len(names) == 0 {
		return []TopicConsumer{}, nil
	}

	described, err := adm.DescribeGroups(ctx, names...)
	if err != nil {
		return nil, fmt.Errorf("describe groups for topic %q on cluster %q: %w", topic, cluster, err)
	}

	candidates := candidatesFromKadm(topic, listed, described)
	if len(candidates) == 0 {
		return []TopicConsumer{}, nil
	}

	ends, endsErr := adm.ListEndOffsets(ctx, topic)
	endsByPartition := map[int32]int64{}
	endsKnown := endsErr == nil
	if endsKnown {
		// Seed end offsets for every partition of the target topic — not only
		// those currently assigned to a member — so groups that are rebalancing
		// (and therefore have no active assignment yet) can still be matched to
		// the topic via their committed offsets further below.
		for p, eo := range ends[topic] {
			endsByPartition[p] = eo.Offset
		}
	}

	fetcher := func(fctx context.Context, group string) (map[int32]int64, error) {
		resp, ferr := adm.FetchOffsetsForTopics(fctx, group, topic)
		if ferr != nil {
			return nil, ferr
		}
		out := map[int32]int64{}
		// Walk all partitions of the target topic in the response.
		if topics, ok := resp[topic]; ok {
			for partition, oe := range topics {
				if oe.Err != nil {
					continue
				}
				out[partition] = oe.At
			}
		}
		return out, nil
	}

	return buildTopicConsumers(ctx, candidates, endsByPartition, endsKnown, fetcher), nil
}

// candidatesFromKadm extracts kadm-typed group descriptions into kadm-free
// inputs that contain only what the business logic needs.
func candidatesFromKadm(topic string, listed kadm.ListedGroups, described kadm.DescribedGroups) []topicConsumerInput {
	out := make([]topicConsumerInput, 0, len(described))
	for name, d := range described {
		state := d.State
		if lg, ok := listed[name]; ok && state == "" {
			state = lg.State
		}
		if state == "Dead" {
			continue
		}
		partsSet := map[int32]struct{}{}
		members := 0
		for _, m := range d.Members {
			ca, ok := m.Assigned.AsConsumer()
			if !ok {
				continue
			}
			memberAssigns := false
			for _, mt := range ca.Topics {
				if mt.Topic != topic {
					continue
				}
				memberAssigns = true
				for _, p := range mt.Partitions {
					partsSet[p] = struct{}{}
				}
			}
			if memberAssigns {
				members++
			}
		}
		hasErr := d.Err != nil
		// Keep the group as a candidate if:
		//   - any member currently assigns partitions of the target topic, OR
		//   - describe failed for this group (surface the error), OR
		//   - the group is between/without assignments (rebalance/empty) — in
		//     which case the committed offsets fetched later tell us whether
		//     the group actually reads from the target topic.
		inRebalance := state == "PreparingRebalance" || state == "CompletingRebalance" || state == "Empty"
		if len(partsSet) == 0 && !hasErr && !inRebalance {
			continue
		}
		parts := make([]int32, 0, len(partsSet))
		for p := range partsSet {
			parts = append(parts, p)
		}
		sort.Slice(parts, func(i, j int) bool { return parts[i] < parts[j] })
		c := topicConsumerInput{
			GroupID:            name,
			State:              state,
			AssigningMembers:   members,
			AssignedPartitions: parts,
		}
		if hasErr {
			c.DescribeErr = classifyErr(d.Err)
		}
		out = append(out, c)
	}
	return out
}

// buildTopicConsumers is the pure business logic, exercised by unit tests.
//
// A candidate is included in the output when at least one of the following
// holds:
//   - it has active partition assignments for the target topic; or
//   - describing it surfaced an error (so the UI can report it); or
//   - it has committed offsets for the target topic (covers groups that are
//     currently rebalancing or temporarily Empty — the active member→topic
//     mapping may be gone, but the committed offsets still witness that the
//     group reads this topic).
//
// When a candidate has no active assignments the partitions are derived from
// the committed-offset fetch so that lag can still be computed for the
// rebalance case.
func buildTopicConsumers(
	ctx context.Context,
	candidates []topicConsumerInput,
	endsByPartition map[int32]int64,
	endsKnown bool,
	fetch offsetFetcher,
) []TopicConsumer {
	out := make([]TopicConsumer, 0, len(candidates))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	var mu sync.Mutex

	for _, c := range candidates {
		c := c
		g.Go(func() error {
			committed, ferr := fetch(gctx, c.GroupID)

			assigned := c.AssignedPartitions
			if len(assigned) == 0 && ferr == nil && len(committed) > 0 {
				assigned = make([]int32, 0, len(committed))
				for p := range committed {
					assigned = append(assigned, p)
				}
				sort.Slice(assigned, func(i, j int) bool { return assigned[i] < assigned[j] })
			}

			// Drop candidates that have no signal at all that they read from
			// the target topic: no active assignment, no committed offsets,
			// and no describe error worth surfacing.
			if len(assigned) == 0 && c.DescribeErr == "" && ferr == nil {
				return nil
			}

			tc := TopicConsumer{
				GroupID:            c.GroupID,
				State:              c.State,
				Members:            c.AssigningMembers,
				PartitionsAssigned: assigned,
				Error:              c.DescribeErr,
			}
			if ferr != nil {
				if tc.Error == "" {
					tc.Error = classifyErr(ferr)
				}
				mu.Lock()
				out = append(out, tc)
				mu.Unlock()
				return nil
			}
			var totalLag int64
			known := endsKnown
			for _, p := range assigned {
				at, hasCommit := committed[p]
				if !hasCommit || at < 0 {
					known = false
					continue
				}
				logEnd, hasEnd := endsByPartition[p]
				if !hasEnd {
					known = false
					continue
				}
				lag := logEnd - at
				if lag < 0 {
					lag = 0
				}
				totalLag += lag
			}
			tc.Lag = totalLag
			tc.LagKnown = known
			mu.Lock()
			out = append(out, tc)
			mu.Unlock()
			return nil
		})
	}
	// errgroup never returns a non-nil error here because callbacks always
	// return nil; Wait is still required to release goroutines.
	_ = g.Wait()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Lag != out[j].Lag {
			return out[i].Lag > out[j].Lag
		}
		return out[i].GroupID < out[j].GroupID
	})
	return out
}

// collectPartitions returns the union of all partition IDs across candidates.
func collectPartitions(candidates []topicConsumerInput) []int32 {
	seen := map[int32]struct{}{}
	for _, c := range candidates {
		for _, p := range c.AssignedPartitions {
			seen[p] = struct{}{}
		}
	}
	out := make([]int32, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
