// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Regression tests for bugs found while pinning the record readers.

// A forward page that runs out of records before the limit used to wait for
// the whole consume timeout, because the "two empty polls" exit never fires:
// PollFetches does not return empty fetches for drained partitions.
func TestConsume_ShortForwardPageDoesNotWaitForTimeout(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	cases := map[string]ConsumeOptions{
		"from start":     {Partition: -1, Limit: 50, From: FromStart},
		"from timestamp": {Partition: -1, Limit: 50, From: FromTimestamp, FromTSMs: fixtureBaseTS + 5_000},
		"from offset":    {Partition: 0, Limit: 50, From: FromOffset, Offset: 3},
		"cursor":         {Partition: -1, Limit: 50, From: FromOffset, PartitionOffsets: map[int32]int64{0: 10, 1: 2, 2: 0}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts.Timeout = 10 * time.Second
			start := time.Now()
			res := consumePage(t, env, opts)
			assert.NotEmpty(t, res.Messages)
			assert.False(t, res.HasMore)
			assert.Less(t, time.Since(start), 3*time.Second, "short forward page waited for the timeout")
		})
	}
}
