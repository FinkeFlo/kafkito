// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Regression tests for #110 item 6b: a stop_on_limit search used to return
// the first limit matches to arrive. When one partition lagged, the page
// held the fast partition's matches alone and the lagging partition's
// earlier (oldest-first) or later (newest-first) matches came pages later.

// wantFirst returns the seqs of the first n records of an m-record fixture
// in the search direction.
func wantFirst(dir SearchDirection, m, n int) []int {
	if dir == DirNewestFirst {
		return seqRange(m-1, m-n)
	}
	return seqRange(0, n-1)
}

func TestSearch_StopOnLimitReturnsTheFirstMatchesWhenAPartitionLags(t *testing.T) {
	t.Parallel()
	for name, pattern := range orderPatterns {
		for _, lagging := range []int32{0, 1} {
			for _, dir := range []SearchDirection{DirOldestFirst, DirNewestFirst} {
				t.Run(fmt.Sprintf("%s/partition %d lags/%s", name, lagging, dir), func(t *testing.T) {
					t.Parallel()
					env := newLaggingPartitionEnv(t, "settled-limit", lagging, everyFetchLags)
					env.produceSeq(t, pattern)
					n := len(pattern)
					opts := SearchOptions{Partition: -1, Limit: 3, StopOnLimit: true, Direction: dir, Value: "seq"}

					first := searchTopic(t, env, opts)
					assert.Equal(t, wantFirst(dir, n, 3), seqs(t, first.Messages), "the globally first matches")
					assert.True(t, first.Stats.MoreAvailable)
					assert.False(t, first.Stats.TimedOut)

					msgs, _ := chainSearch(t, env, opts)
					assert.Equal(t, wantFirst(dir, n, n), seqs(t, msgs), "every hit once, in order")
				})
			}
		}
	}
}

// While a stop_on_limit call waits for a lagging partition, the partitions
// already past the page's last match are skipped and do not spend the
// budget. Newest-first reads chunks of budget/partitions offsets, one per
// poll, so without skipping the fast partition would spend this budget
// before the lagging partition answers.
func TestSearch_StopOnLimitWaitsWithoutSpendingTheBudget(t *testing.T) {
	t.Parallel()
	for name, pattern := range orderPatterns {
		for _, lagging := range []int32{0, 1} {
			t.Run(fmt.Sprintf("%s/partition %d lags", name, lagging), func(t *testing.T) {
				t.Parallel()
				env := newLaggingPartitionEnv(t, "settled-parked", lagging, everyFetchLags)
				env.produceSeq(t, pattern)
				n := len(pattern)
				opts := SearchOptions{Partition: -1, Limit: 3, Budget: 8, StopOnLimit: true, Value: "seq"}

				first := searchTopic(t, env, opts)
				assert.Equal(t, wantFirst(DirNewestFirst, n, 3), seqs(t, first.Messages), "the globally first matches")
				assert.True(t, first.Stats.MoreAvailable)

				msgs, _ := chainSearch(t, env, opts)
				assert.Equal(t, wantFirst(DirNewestFirst, n, n), seqs(t, msgs), "every hit once, in order")
			})
		}
	}
}

// A budget still ends a stop_on_limit call before the lagging partition
// delivers. The chain then still returns every hit exactly once; the order
// across calls is not pinned.
func TestSearch_StopOnLimitBudgetChainCoversALaggingPartition(t *testing.T) {
	t.Parallel()
	for name, pattern := range orderPatterns {
		for _, dir := range []SearchDirection{DirOldestFirst, DirNewestFirst} {
			t.Run(fmt.Sprintf("%s/%s", name, dir), func(t *testing.T) {
				t.Parallel()
				env := newLaggingPartitionEnv(t, "settled-budget", 1, everyFetchLags)
				env.produceSeq(t, pattern)
				opts := SearchOptions{Partition: -1, Limit: 3, Budget: 4, StopOnLimit: true, Direction: dir, Value: "seq"}

				first := searchTopic(t, env, opts)
				assert.True(t, first.Stats.BudgetExhausted)
				assert.True(t, first.Stats.MoreAvailable)
				assert.LessOrEqual(t, len(first.Messages), 3)

				msgs, _ := chainSearch(t, env, opts)
				assert.ElementsMatch(t, seqRange(0, len(pattern)-1), seqs(t, msgs), "every hit exactly once")
			})
		}
	}
}

// A stop_on_limit call waits for a lagging partition up to its timeout. It
// then returns what it has, reports more, and the chain loses nothing.
func TestSearch_StopOnLimitTimeoutKeepsTheLaggingPartitionForTheNextCall(t *testing.T) {
	t.Parallel()
	for _, dir := range []SearchDirection{DirOldestFirst, DirNewestFirst} {
		t.Run(string(dir), func(t *testing.T) {
			t.Parallel()
			var hold atomic.Bool
			hold.Store(true)
			env := newLaggingPartitionEnv(t, "settled-timeout", 1, func(int) time.Duration {
				if hold.Load() {
					return 20 * time.Second
				}
				return 0
			})
			pattern := orderPatterns["alternating"]
			env.produceSeq(t, pattern)
			opts := SearchOptions{Partition: -1, Limit: 3, StopOnLimit: true, Direction: dir, Value: "seq", Timeout: 2 * time.Second}

			first := searchTopic(t, env, opts)
			hold.Store(false)
			assert.True(t, first.Stats.TimedOut, "the call waits for the lagging partition")
			assert.True(t, first.Stats.MoreAvailable)
			unread := first.Stats.ResolvedRange[1].End
			if dir == DirOldestFirst {
				unread = first.Stats.ResolvedRange[1].Start
			}
			assert.Equal(t, unread, first.Stats.NextCursors[1], "the unread partition keeps its range")

			opts.Cursors = first.Stats.NextCursors
			msgs, _ := chainSearch(t, env, opts)
			require.NotEmpty(t, msgs)
			assert.ElementsMatch(t, seqRange(0, len(pattern)-1), append(seqs(t, first.Messages), seqs(t, msgs)...),
				"every hit exactly once")
		})
	}
}

// visitAt visits a record of partition p at offset off with timestamp ts
// (millis) and value v.
func visitAt(sc *searchScan, p int32, off, ts int64, v string) {
	sc.visit(context.Background(), &kgo.Record{Partition: p, Offset: off, Timestamp: time.UnixMilli(ts), Value: []byte(v)})
}

func TestSearchScan_SettledOldestFirst(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 10}, 1: {Start: 0, End: 10}}
	sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
	for i, ts := range []int64{10, 30, 50} {
		visitAt(sc, 0, int64(i), ts, "a")
	}
	assert.False(t, sc.settled(3), "partition 1 read nothing yet")

	visitAt(sc, 1, 0, 20, "bad")
	assert.False(t, sc.settled(3), "partition 1's next record can still rank before ts 50")

	visitAt(sc, 1, 1, 60, "bad")
	assert.True(t, sc.settled(3), "partition 1 read past the page's last match")

	sc = newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
	for i, ts := range []int64{10, 30, 50} {
		visitAt(sc, 0, int64(i), ts, "a")
	}
	sc.finish(recordBatch{drained: []int32{1}})
	assert.True(t, sc.settled(3), "a drained partition never blocks")
}

func TestSearchScan_SettledNewestFirst(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 10}, 1: {Start: 0, End: 10}}
	newScan := func() *searchScan {
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
		for i, ts := range []int64{70, 80, 90} {
			visitAt(sc, 0, int64(7+i), ts, "a")
		}
		sc.finish(recordBatch{chunks: []finishedChunk{{partition: 0, lower: 7}}})
		return sc
	}

	sc := newScan()
	assert.False(t, sc.settled(3), "partition 1 read nothing yet")

	visitAt(sc, 1, 5, 60, "bad")
	visitAt(sc, 1, 9, 95, "bad")
	assert.False(t, sc.settled(3), "a chunk not read to its end does not count")

	sc.finish(recordBatch{chunks: []finishedChunk{{partition: 1, lower: 5}}})
	assert.True(t, sc.settled(3), "partition 1's frontier ranks after the page's last match")

	sc = newScan()
	visitAt(sc, 1, 8, 75, "bad")
	sc.finish(recordBatch{chunks: []finishedChunk{{partition: 1, lower: 8}}})
	assert.False(t, sc.settled(3), "partition 1's next record can still rank before ts 70")

	sc = newScan()
	sc.finish(recordBatch{chunks: []finishedChunk{{partition: 1, lower: 8, forced: true}}, drained: []int32{1}})
	assert.True(t, sc.settled(3), "a partition the reader gave up on never blocks")
}

// A parked partition's records are skipped, so the end of its range or of
// its chunk must not move its cursor past them.
func TestSearchScan_ParkedPartitionKeepsItsCursor(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 10}, 1: {Start: 0, End: 10}}

	t.Run("oldest first", func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
		for i, ts := range []int64{10, 30, 50} {
			visitAt(sc, 0, int64(i), ts, "a")
		}
		assert.False(t, sc.settled(3), "partition 1 read nothing yet")
		assert.True(t, sc.parked[0])

		sc.finish(recordBatch{drained: []int32{0}, ended: []int32{0}})
		next, more := sc.continuation(ranges)
		assert.Equal(t, int64(3), next[0], "the skipped records stay for the next call")
		assert.True(t, more)
	})

	t.Run("newest first", func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
		for i, ts := range []int64{70, 80, 90} {
			visitAt(sc, 0, int64(7+i), ts, "a")
		}
		sc.finish(recordBatch{chunks: []finishedChunk{{partition: 0, lower: 7}}})
		assert.False(t, sc.settled(3), "partition 1 read nothing yet")
		assert.True(t, sc.parked[0])

		sc.finish(recordBatch{chunks: []finishedChunk{{partition: 0, lower: 4}}})
		next, _ := sc.continuation(ranges)
		assert.Equal(t, int64(7), next[0], "the skipped chunk stays for the next call")
	})
}
