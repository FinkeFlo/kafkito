// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kgo"
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

// A record that fails to parse at the very end of a scanned range used to
// skip the "range done" bookkeeping, so the search waited for its whole
// timeout and reported timed_out even though every record had been scanned.
func TestSearch_ParseErrorOnLastRecordDoesNotWaitForTimeout(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "tail-broken", 1, nil)
	for i, v := range []string{`{"a":1}`, `{"a":2}`, `{broken`} {
		env.produce(t, &kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)), Value: []byte(v)})
	}
	queries := map[string]SearchOptions{
		"jsonpath": {Mode: SearchModeJSONPath, Path: "$.a", Op: OpGte, Value: "1"},
		"js":       {Mode: SearchModeJS, Value: "JSON.parse(value).a >= 1"},
	}
	for name, q := range queries {
		for _, dir := range []SearchDirection{DirOldestFirst, DirNewestFirst} {
			t.Run(name+"/"+string(dir), func(t *testing.T) {
				t.Parallel()
				q.Partition, q.Limit, q.Direction, q.Timeout = -1, 500, dir, 10*time.Second
				start := time.Now()
				res := searchTopic(t, env, q)
				assert.Less(t, time.Since(start), 3*time.Second, "search waited for the timeout")
				assert.False(t, res.Stats.TimedOut)
				assert.False(t, res.Stats.MoreAvailable)
				assert.Equal(t, 3, res.Stats.Scanned)
				assert.Equal(t, 2, res.Stats.Matched)
				assert.Equal(t, 1, res.Stats.ParseErrors)
			})
		}
	}
}
