// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFairShare(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		limit       int
		partitions  int
		wantPerPart int
	}{
		{"limit 50 / 3 partitions", 50, 3, 17 + balanceBuffer}, // ceil(50/3)=17
		{"limit 50 / 2 partitions", 50, 2, 25 + balanceBuffer},
		{"limit 50 / 1 partition", 50, 1, 50},                     // single = exactly limit, no buffer
		{"limit 1 / 3 partitions", 1, 3, 1 + balanceBuffer},
		{"limit 500 / 3 partitions", 500, 3, 167 + balanceBuffer}, // ceil(500/3)=167
		{"limit 100 / 100 partitions", 100, 100, 1 + balanceBuffer},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.wantPerPart, fairShare(tc.limit, tc.partitions))
		})
	}
}

func TestFairShare_ZeroPartitions(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, fairShare(50, 0))
	require.Equal(t, 0, fairShare(0, 3))
	require.Equal(t, 0, fairShare(-5, 3))
}
