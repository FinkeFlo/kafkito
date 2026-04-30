// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildTopicConsumers(t *testing.T) {
	t.Parallel()

	type fixture struct {
		name      string
		inputs    []topicConsumerInput
		ends      map[int32]int64
		endsKnown bool
		fetch     offsetFetcher
		want      []TopicConsumer
	}

	okFetcher := func(committed map[string]map[int32]int64) offsetFetcher {
		return func(_ context.Context, group string) (map[int32]int64, error) {
			if v, ok := committed[group]; ok {
				return v, nil
			}
			return map[int32]int64{}, nil
		}
	}

	cases := []fixture{
		{
			name:   "empty",
			inputs: nil,
			want:   []TopicConsumer{},
		},
		{
			name: "happy_path_sort_by_lag_desc",
			inputs: []topicConsumerInput{
				{GroupID: "alpha", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0, 1}},
				{GroupID: "beta", State: "Stable", AssigningMembers: 2, AssignedPartitions: []int32{0, 1, 2}},
				{GroupID: "gamma", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0}},
			},
			ends:      map[int32]int64{0: 100, 1: 200, 2: 50},
			endsKnown: true,
			fetch: okFetcher(map[string]map[int32]int64{
				"alpha": {0: 90, 1: 195}, // lag 10+5=15
				"beta":  {0: 0, 1: 0, 2: 0},
				"gamma": {0: 100},
			}),
			want: []TopicConsumer{
				{GroupID: "beta", State: "Stable", Members: 2, PartitionsAssigned: []int32{0, 1, 2}, Lag: 350, LagKnown: true},
				{GroupID: "alpha", State: "Stable", Members: 1, PartitionsAssigned: []int32{0, 1}, Lag: 15, LagKnown: true},
				{GroupID: "gamma", State: "Stable", Members: 1, PartitionsAssigned: []int32{0}, Lag: 0, LagKnown: true},
			},
		},
		{
			name: "tie_lag_sorts_by_group_id_asc",
			inputs: []topicConsumerInput{
				{GroupID: "z-team", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0}},
				{GroupID: "a-team", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0}},
			},
			ends:      map[int32]int64{0: 50},
			endsKnown: true,
			fetch: okFetcher(map[string]map[int32]int64{
				"z-team": {0: 25}, // lag 25
				"a-team": {0: 25}, // lag 25
			}),
			want: []TopicConsumer{
				{GroupID: "a-team", State: "Stable", Members: 1, PartitionsAssigned: []int32{0}, Lag: 25, LagKnown: true},
				{GroupID: "z-team", State: "Stable", Members: 1, PartitionsAssigned: []int32{0}, Lag: 25, LagKnown: true},
			},
		},
		{
			name: "ends_unknown_marks_lag_unknown",
			inputs: []topicConsumerInput{
				{GroupID: "g1", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0}},
			},
			ends:      nil,
			endsKnown: false,
			fetch: okFetcher(map[string]map[int32]int64{
				"g1": {0: 10},
			}),
			want: []TopicConsumer{
				{GroupID: "g1", State: "Stable", Members: 1, PartitionsAssigned: []int32{0}, Lag: 0, LagKnown: false},
			},
		},
		{
			name: "missing_commit_marks_lag_unknown_per_partition",
			inputs: []topicConsumerInput{
				{GroupID: "g1", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0, 1}},
			},
			ends:      map[int32]int64{0: 100, 1: 100},
			endsKnown: true,
			fetch: okFetcher(map[string]map[int32]int64{
				"g1": {0: 90}, // partition 1 has no commit
			}),
			want: []TopicConsumer{
				{GroupID: "g1", State: "Stable", Members: 1, PartitionsAssigned: []int32{0, 1}, Lag: 10, LagKnown: false},
			},
		},
		{
			name: "fetch_error_surfaces_per_group",
			inputs: []topicConsumerInput{
				{GroupID: "ok", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0}},
				{GroupID: "broken", State: "Stable", AssigningMembers: 1, AssignedPartitions: []int32{0}},
			},
			ends:      map[int32]int64{0: 10},
			endsKnown: true,
			fetch: func(_ context.Context, group string) (map[int32]int64, error) {
				if group == "broken" {
					return nil, errors.New("not authorized")
				}
				return map[int32]int64{0: 5}, nil
			},
			want: []TopicConsumer{
				{GroupID: "ok", State: "Stable", Members: 1, PartitionsAssigned: []int32{0}, Lag: 5, LagKnown: true},
				{GroupID: "broken", State: "Stable", Members: 1, PartitionsAssigned: []int32{0}, Lag: 0, LagKnown: false, Error: "not authorized"},
			},
		},
		{
			name: "describe_error_preserved_over_fetch_error",
			inputs: []topicConsumerInput{
				{GroupID: "g1", State: "Stable", AssigningMembers: 0, AssignedPartitions: []int32{0}, DescribeErr: "describe failed"},
			},
			ends:      map[int32]int64{0: 10},
			endsKnown: true,
			fetch: func(_ context.Context, _ string) (map[int32]int64, error) {
				return nil, errors.New("fetch failed")
			},
			want: []TopicConsumer{
				{GroupID: "g1", State: "Stable", Members: 0, PartitionsAssigned: []int32{0}, Lag: 0, LagKnown: false, Error: "describe failed"},
			},
		},
		{
			// A rebalancing group has no active assignments yet, but its
			// committed offsets still witness that it reads from the topic.
			// It must appear in the topic-consumers list, with partitions and
			// lag derived from the committed offsets.
			name: "rebalancing_group_included_via_committed_offsets",
			inputs: []topicConsumerInput{
				{GroupID: "rebalancer", State: "PreparingRebalance", AssigningMembers: 0, AssignedPartitions: nil},
			},
			ends:      map[int32]int64{0: 100, 1: 200, 2: 50},
			endsKnown: true,
			fetch: okFetcher(map[string]map[int32]int64{
				"rebalancer": {0: 80, 1: 190, 2: 50}, // lag 20+10+0 = 30
			}),
			want: []TopicConsumer{
				{GroupID: "rebalancer", State: "PreparingRebalance", Members: 0, PartitionsAssigned: []int32{0, 1, 2}, Lag: 30, LagKnown: true},
			},
		},
		{
			// A rebalancing group without any committed offsets for the topic
			// is not actually a consumer of this topic; it must be dropped so
			// the list stays accurate.
			name: "rebalancing_group_without_commits_excluded",
			inputs: []topicConsumerInput{
				{GroupID: "other-topic-group", State: "PreparingRebalance", AssigningMembers: 0, AssignedPartitions: nil},
			},
			ends:      map[int32]int64{0: 100},
			endsKnown: true,
			fetch:     okFetcher(map[string]map[int32]int64{}),
			want:      []TopicConsumer{},
		},
		{
			// An Empty group may have no members at all but still own committed
			// offsets for this topic (paused consumer). Include it.
			name: "empty_state_with_commits_included",
			inputs: []topicConsumerInput{
				{GroupID: "paused", State: "Empty", AssigningMembers: 0, AssignedPartitions: nil},
			},
			ends:      map[int32]int64{0: 10, 1: 20},
			endsKnown: true,
			fetch: okFetcher(map[string]map[int32]int64{
				"paused": {0: 5, 1: 20}, // lag 5+0 = 5
			}),
			want: []TopicConsumer{
				{GroupID: "paused", State: "Empty", Members: 0, PartitionsAssigned: []int32{0, 1}, Lag: 5, LagKnown: true},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildTopicConsumers(context.Background(), tc.inputs, tc.ends, tc.endsKnown, tc.fetch)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCollectPartitions(t *testing.T) {
	t.Parallel()
	got := collectPartitions([]topicConsumerInput{
		{AssignedPartitions: []int32{2, 0}},
		{AssignedPartitions: []int32{0, 5, 1}},
	})
	require.Equal(t, []int32{0, 1, 2, 5}, got)
}
