// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"fmt"
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
				opts := q
				opts.Partition, opts.Limit, opts.Direction, opts.Timeout = -1, 500, dir, 10*time.Second
				start := time.Now()
				res := searchTopic(t, env, opts)
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

// Forward cursor pages ("Load more" in a time range, every copy-job page
// after the first) used to ignore the upper time bound, so paging leaked
// records at or after to_ts and has_more stayed true until the topic end.
func TestConsume_ForwardCursorPagesKeepUpperTimeBound(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	for name, from := range map[string]ConsumeFrom{"from timestamp": FromTimestamp, "from start": FromStart} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts := ConsumeOptions{Partition: -1, Limit: 3, From: from, FromTSMs: fixtureBaseTS + 2_000, ToTSMs: fixtureBaseTS + 14_000}
			pages := consumeAllPages(t, env, opts)
			var got []int
			for _, p := range pageSeqs(t, pages) {
				got = append(got, p...)
			}
			assert.Equal(t, seqRange(2, 13), got)
			assert.False(t, pages[len(pages)-1].HasMore)
		})
	}
}

// A from=end page caps every partition at its fair share (limit/K + 8). When
// one partition holds most of the newest records, the cap used to cut it off
// and the page was filled with older records of other partitions instead,
// so the page was not the newest N and later pages broke newest-first order.
func TestConsume_FromEndDominantPartitionIsNotCappedByFairShare(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "bursts", 3, nil)
	// Bursts: p0 oldest, then p2, then p1 with the 30 newest records.
	burst := func(p int32, n int, baseTS int64) {
		for i := range n {
			env.produce(t, &kgo.Record{
				Partition: p,
				Timestamp: time.UnixMilli(baseTS + int64(i)),
				Value:     []byte(fmt.Sprintf("p%d-%d", p, i)),
			})
		}
	}
	burst(0, 20, fixtureBaseTS)
	burst(2, 10, fixtureBaseTS+1_000)
	burst(1, 30, fixtureBaseTS+2_000)

	opts := ConsumeOptions{Partition: -1, Limit: 30, From: FromEnd}
	first := consumePage(t, env, opts)
	assert.Equal(t, valuesOf("p1", 29), values(first.Messages), "the newest 30 records are all on p1")
	assert.False(t, first.Partial)

	var all []string
	for _, page := range consumeAllPages(t, env, opts) {
		all = append(all, values(page.Messages)...)
	}
	want := append(append(valuesOf("p1", 29), valuesOf("p2", 9)...), valuesOf("p0", 19)...)
	assert.Equal(t, want, all, "paging returns every record once, newest first")
}

func values(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Value)
	}
	return out
}

// valuesOf returns "<prefix>-<i>" for i from hi down to 0.
func valuesOf(prefix string, hi int) []string {
	var out []string
	for i := hi; i >= 0; i-- {
		out = append(out, fmt.Sprintf("%s-%d", prefix, i))
	}
	return out
}
