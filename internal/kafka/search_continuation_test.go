// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Regression tests for search continuation (#110): next_cursors must never
// point past an offset that was not read, and more_available must stay true
// while any partition of the range was not read to its end.

// messageOffsets returns the offsets of msgs in order.
func messageOffsets(msgs []Message) []int64 {
	out := []int64{}
	for _, m := range msgs {
		out = append(out, m.Offset)
	}
	return out
}

// A partition the search never got a record from used to count as fully
// scanned. When the per-call timeout hit before the lagging partition's
// first fetch returned, the search reported more_available=false and its
// records were never searched.
func TestSearch_TimeoutKeepsAnUnreadPartitionForTheNextCall(t *testing.T) {
	t.Parallel()
	for _, dir := range []SearchDirection{DirNewestFirst, DirOldestFirst} {
		t.Run(string(dir), func(t *testing.T) {
			t.Parallel()
			// Every fetch of partition 1 is held far beyond the timeout during
			// the first call. The client re-sends a held fetch when it moves
			// partition 0 to its next chunk, so holding only the first fetch
			// is not enough.
			var hold atomic.Bool
			hold.Store(true)
			env := newLaggingPartitionEnv(t, "timeout-unread", 1, func(int) time.Duration {
				if hold.Load() {
					return 20 * time.Second
				}
				return 0
			})
			env.produceSeq(t, alternating(6))
			opts := SearchOptions{Partition: -1, Limit: 500, Direction: dir, Value: "seq", Timeout: 2 * time.Second}

			first := searchTopic(t, env, opts)
			hold.Store(false)
			assert.True(t, first.Stats.TimedOut)
			assert.True(t, first.Stats.MoreAvailable, "the unread partition is still to be searched")
			unread := first.Stats.ResolvedRange[1].End
			if dir == DirOldestFirst {
				unread = first.Stats.ResolvedRange[1].Start
			}
			assert.Equal(t, unread, first.Stats.NextCursors[1], "the unread partition keeps its range")

			msgs, _ := chainSearch(t, env, opts)
			assert.ElementsMatch(t, seqRange(0, 5), seqs(t, msgs), "the chain returns every hit exactly once")
		})
	}
}

// A newest-first search that stopped in the middle of a chunk moved its
// cursor to the lowest offset read, so the unread upper part of the chunk
// was skipped. Here every fetch carries one record and the budget ends a
// call after two records.
func TestSearch_NewestFirstBudgetStopDoesNotSkipTheRestOfAChunk(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "newest-partial-chunk", 1, nil)
	// Incompressible, so that one fetch cannot carry two of these records.
	big := make([]byte, 700<<10)
	_, _ = rand.NewChaCha8([32]byte{}).Read(big)
	for i := range 5 {
		env.produce(t, &kgo.Record{
			Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)*1000),
			Key:       fmt.Appendf(nil, "k-%d", i),
			Value:     big,
		})
	}

	msgs, calls := chainSearch(t, env, SearchOptions{Partition: -1, Limit: 500, Budget: 2, Value: "k-", Zones: []SearchZone{ZoneKey}})

	assert.Equal(t, []int64{4, 3, 2, 1, 0}, messageOffsets(msgs), "every record once, newest first")
	require.NotEmpty(t, calls)
	assert.True(t, calls[0].BudgetExhausted)
}

// Oldest-first moved its cursor to the offset after the last record. When
// the range ended in a transaction marker, that cursor stayed below the end
// and the search reported more_available although everything was read.
func TestSearch_OldestFirstRangeEndingInAMarkerIsComplete(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "oldest-marker", 1, nil)
	// Offsets 0, 2, 4 hold the records, 1, 3, 5 the commit markers.
	env.produceTransactional(t,
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS), Value: []byte("r-0")},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 1000), Value: []byte("r-1")},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 2000), Value: []byte("r-2")},
	)

	for _, dir := range []SearchDirection{DirOldestFirst, DirNewestFirst} {
		res := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 50, Direction: dir, Value: "r-"})
		assert.Len(t, res.Messages, 3, "direction %s", dir)
		assert.False(t, res.Stats.MoreAvailable, "direction %s", dir)
		want := map[int32]int64{0: 6}
		if dir == DirNewestFirst {
			want = map[int32]int64{0: 0}
		}
		assert.Equal(t, want, res.Stats.NextCursors, "direction %s", dir)
	}
}

// With a lagging partition the budget used to run out on the fast
// partition's records alone, and the lagging partition counted as fully
// searched. A chain must return every hit; their order across calls is not
// pinned here.
func TestSearch_BudgetChainCoversALaggingPartition(t *testing.T) {
	t.Parallel()
	for name, pattern := range orderPatterns {
		for _, dir := range []SearchDirection{DirNewestFirst, DirOldestFirst} {
			t.Run(fmt.Sprintf("%s/%s", name, dir), func(t *testing.T) {
				t.Parallel()
				env := newLaggingPartitionEnv(t, "budget-lag", 1, everyFetchLags)
				env.produceSeq(t, pattern)

				msgs, _ := chainSearch(t, env, SearchOptions{Partition: -1, Limit: 500, Budget: 4, Direction: dir, Value: "seq"})

				assert.ElementsMatch(t, seqRange(0, len(pattern)-1), seqs(t, msgs))
			})
		}
	}
}

// errOnMatcher fails to evaluate the records whose value is "bad" and
// matches every other record.
type errOnMatcher struct{}

func (errOnMatcher) match(m *Message) (bool, error) {
	if m.Value == "bad" {
		return false, errors.New("bad value")
	}
	return true, nil
}

// feed visits recs of partition 0 and applies batch to sc.
func feed(t *testing.T, sc *searchScan, batch recordBatch, values map[int64]string) {
	t.Helper()
	for off, v := range values {
		batch.records = append(batch.records, &kgo.Record{Partition: 0, Offset: off, Value: []byte(v)})
	}
	for _, rec := range batch.records {
		sc.visit(context.Background(), rec)
	}
	sc.finish(batch)
}

func TestSearchScan_NewestFirstConfirmsOnlyFinishedChunks(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 12}}

	t.Run("a chunk cut short by a stop keeps the cursor above it", func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
		feed(t, sc, recordBatch{chunks: []finishedChunk{{partition: 0, lower: 8}}}, map[int64]string{8: "a", 9: "bad", 10: "b", 11: "c"})
		feed(t, sc, recordBatch{}, map[int64]string{4: "d", 5: "bad"}) // the stop comes here

		assert.Equal(t, 3, sc.confirmed)
		assert.Equal(t, []int64{8, 10, 11}, sortedOffsets(sc.confirmedMatches()))
		assert.Equal(t, 1, sc.parseErrors, "the parse error of the unfinished chunk is reported by the call that finishes it")
		assert.Equal(t, []ParseErrorOffset{{Partition: 0, Offset: 9, Error: "bad value"}}, sc.parseErrorOffsets)
		next, more := sc.continuation(ranges)
		assert.Equal(t, map[int32]int64{0: 8}, next)
		assert.True(t, more)
	})

	t.Run("a forced chunk is a hole the frontier never passes", func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
		feed(t, sc, recordBatch{chunks: []finishedChunk{{partition: 0, lower: 8}}}, map[int64]string{8: "a", 11: "b"})
		feed(t, sc, recordBatch{chunks: []finishedChunk{{partition: 0, lower: 4, forced: true}}}, map[int64]string{4: "c"})
		feed(t, sc, recordBatch{chunks: []finishedChunk{{partition: 0, lower: 0}}}, map[int64]string{0: "d"})

		assert.Equal(t, []int64{8, 11}, sortedOffsets(sc.confirmedMatches()))
		next, more := sc.continuation(ranges)
		assert.Equal(t, map[int32]int64{0: 8}, next)
		assert.True(t, more)
	})

	t.Run("an unvisited partition keeps its range", func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
		next, more := sc.continuation(ranges)
		assert.Equal(t, map[int32]int64{0: 12}, next)
		assert.True(t, more)
	})
}

func TestSearchScan_OldestFirstContinuesAfterTheHighestRecord(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 2, End: 12}}

	sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
	next, more := sc.continuation(ranges)
	assert.Equal(t, map[int32]int64{0: 2}, next, "an unvisited partition keeps its range")
	assert.True(t, more)

	feed(t, sc, recordBatch{}, map[int64]string{2: "a", 3: "bad", 4: "b"})
	assert.Equal(t, 2, sc.confirmed)
	assert.Equal(t, 1, sc.parseErrors)
	next, more = sc.continuation(ranges)
	assert.Equal(t, map[int32]int64{0: 5}, next)
	assert.True(t, more)

	feed(t, sc, recordBatch{drained: []int32{0}, ended: []int32{0}}, map[int64]string{10: "c"})
	next, more = sc.continuation(ranges)
	assert.Equal(t, map[int32]int64{0: 12}, next, "a range read to its end is done even if a marker ends it")
	assert.False(t, more)
}

func sortedOffsets(msgs []Message) []int64 {
	out := messageOffsets(msgs)
	slices.Sort(out)
	return out
}

// A newest-first stop-on-limit search stops only after a chunk is read to
// its end, so its cursor never skips the rest of a chunk and the chain
// returns every hit once, newest first. Every chunk holds exactly Limit
// hits here: hits cut off by the limit are #110 item 1, not pinned here.
func TestSearch_NewestFirstStopOnLimitDoesNotSkipTheRestOfAChunk(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "newest-limit-chunk", 1, nil)
	env.produceSeq(t, make([]int32, 10))

	var got []int
	opts := SearchOptions{Partition: -1, Limit: 2, Budget: 4, StopOnLimit: true, Mode: SearchModeJS, Value: "parsed.seq % 2 === 0"}
	for range 20 {
		res := searchTopic(t, env, opts)
		got = append(got, seqs(t, res.Messages)...)
		if !res.Stats.MoreAvailable {
			break
		}
		opts.Cursors = res.Stats.NextCursors
	}
	assert.Equal(t, []int{8, 6, 4, 2, 0}, got)
}
