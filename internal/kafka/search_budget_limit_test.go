// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Regression tests for #110 item 1: the budget holds per record, a hit cut
// off by the limit stays reachable through next_cursors, and scanned and
// matched count only what the cursor moved past.

// The budget used to be checked after every poll, and one poll can carry a
// whole partition: Budget 10 scanned 100 records.
func TestSearch_BudgetHoldsPerRecord(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "budget-per-record", 1, nil)
	env.produceSeq(t, make([]int32, 100))

	for _, dir := range []SearchDirection{DirNewestFirst, DirOldestFirst} {
		t.Run(string(dir), func(t *testing.T) {
			t.Parallel()
			opts := SearchOptions{Partition: -1, Limit: 500, Budget: 10, Direction: dir, Value: "seq"}

			first := searchTopic(t, env, opts)
			assert.Equal(t, 10, first.Stats.Scanned)
			assert.Equal(t, 10, first.Stats.Read)
			assert.True(t, first.Stats.BudgetExhausted)
			assert.True(t, first.Stats.MoreAvailable)

			msgs, calls := chainSearch(t, env, opts)
			offsets := messageOffsets(msgs)
			want := seqRange(0, 99)
			if dir == DirNewestFirst {
				want = seqRange(99, 0)
			}
			assert.Equal(t, want, seqs(t, msgs), "every offset once, in order")
			assert.Len(t, offsets, 100)
			scanned, matched := 0, 0
			for _, c := range calls {
				assert.LessOrEqual(t, c.Read, 10)
				assert.LessOrEqual(t, c.Scanned, c.Read)
				scanned += c.Scanned
				matched += c.Matched
			}
			assert.Equal(t, 100, scanned, "scanned adds up to the records across the chain")
			assert.Equal(t, 100, matched, "matched adds up to the hits across the chain")
		})
	}
}

// Hits beyond the limit used to be dropped while the cursor moved past
// them, so the next call never returned them.
func TestSearch_LimitKeepsTruncatedHitsReachable(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	for _, stopOnLimit := range []bool{true, false} {
		for _, dir := range []SearchDirection{DirNewestFirst, DirOldestFirst} {
			t.Run(fmt.Sprintf("stop_on_limit=%t/%s", stopOnLimit, dir), func(t *testing.T) {
				t.Parallel()
				opts := SearchOptions{Partition: -1, Limit: 3, StopOnLimit: stopOnLimit, Direction: dir, Value: "rec"}

				msgs, calls := chainSearch(t, env, opts)

				assert.ElementsMatch(t, seqRange(0, 23), seqs(t, msgs), "every hit once")
				scanned, matched := 0, 0
				for i, c := range calls {
					scanned += c.Scanned
					matched += c.Matched
					if i < len(calls)-1 {
						assert.Equal(t, 3, c.Matched, "call %d returns a full page", i)
					}
				}
				assert.Len(t, calls, 8, "24 hits in pages of 3")
				assert.Equal(t, 24, scanned)
				assert.Equal(t, 24, matched)
			})
		}
	}
}

// visitAll visits recs in order and applies batch.
func visitAll(sc *searchScan, batch recordBatch, recs ...*kgo.Record) {
	for _, rec := range recs {
		sc.visit(context.Background(), rec)
	}
	batch.records = recs
	sc.finish(batch)
}

func rec(p int32, off, ts int64, v string) *kgo.Record {
	return &kgo.Record{Partition: p, Offset: off, Timestamp: time.UnixMilli(ts), Value: []byte(v)}
}

// With timestamps out of order within a partition, the page used to be
// cut by time alone. Keeping each partition's page an offset prefix makes
// every call with a hit move its cursor.
func TestSearchScan_PageIsAnOffsetPrefixPerPartition(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 4}}

	t.Run(string(DirOldestFirst), func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
		visitAll(sc, recordBatch{drained: []int32{0}, ended: []int32{0}},
			rec(0, 0, 50, "a"), rec(0, 1, 10, "b"), rec(0, 2, 20, "c"), rec(0, 3, 30, "d"))

		pg := sc.page(ranges, 2)
		assert.Equal(t, []int64{1, 0}, messageOffsets(pg.messages), "offsets 0 and 1, oldest first")
		assert.Equal(t, map[int32]int64{0: 2}, pg.next)
		assert.True(t, pg.more)
		assert.Equal(t, 2, pg.scanned)
		assert.Equal(t, 2, pg.matched)
	})

	t.Run(string(DirNewestFirst), func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
		visitAll(sc, recordBatch{chunks: []finishedChunk{{partition: 0, lower: 0}}},
			rec(0, 0, 50, "a"), rec(0, 1, 10, "b"), rec(0, 2, 20, "c"), rec(0, 3, 5, "d"))

		pg := sc.page(ranges, 2)
		assert.Equal(t, []int64{2, 3}, messageOffsets(pg.messages), "offsets 3 and 2, newest first")
		assert.Equal(t, map[int32]int64{0: 2}, pg.next)
		assert.True(t, pg.more)
		assert.Equal(t, 2, pg.scanned)
		assert.Equal(t, 2, pg.matched)
	})
}

func TestSearchScan_PageMergesPartitionsByTime(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 2}, 1: {Start: 0, End: 2}}
	sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
	visitAll(sc, recordBatch{drained: []int32{0, 1}, ended: []int32{0, 1}},
		rec(0, 0, 10, "a"), rec(0, 1, 20, "b"), rec(1, 0, 15, "c"), rec(1, 1, 25, "d"))

	pg := sc.page(ranges, 3)

	assert.Equal(t, []int64{0, 0, 1}, messageOffsets(pg.messages))
	assert.Equal(t, map[int32]int64{0: 2, 1: 1}, pg.next)
	assert.True(t, pg.more)
	assert.Equal(t, 3, pg.scanned)
	assert.Equal(t, 3, pg.matched)
}

// Parse errors count up to the cursor too; the next call reads the ones
// above it again.
func TestSearchScan_ParseErrorsCountUpToTheCursor(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 5}}
	sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
	visitAll(sc, recordBatch{drained: []int32{0}, ended: []int32{0}},
		rec(0, 0, 1, "a"), rec(0, 1, 2, "bad"), rec(0, 2, 3, "b"), rec(0, 3, 4, "bad"), rec(0, 4, 5, "c"))

	pg := sc.page(ranges, 1)

	assert.Equal(t, map[int32]int64{0: 2}, pg.next)
	assert.Equal(t, 2, pg.scanned)
	assert.Equal(t, 1, pg.parseErrors)
	assert.Equal(t, []ParseErrorOffset{{Partition: 0, Offset: 1, Error: "bad value"}}, pg.parseErrorOffsets)
	assert.Equal(t, 5, sc.read)
}

// A stop in the middle of a poll leaves the chunk and the range of every
// partition with an unvisited record in that poll open.
func TestCutBatch(t *testing.T) {
	t.Parallel()
	batch := recordBatch{
		records: []*kgo.Record{rec(0, 8, 0, ""), rec(1, 3, 0, ""), rec(0, 9, 0, ""), rec(2, 1, 0, "")},
		drained: []int32{0, 1, 2, 3},
		ended:   []int32{0, 1, 2, 3},
		chunks:  []finishedChunk{{partition: 0, lower: 8}, {partition: 1, lower: 0}, {partition: 3, lower: 0, forced: true}},
	}

	cut := cutBatch(batch, 2)

	require.Len(t, cut.records, 2)
	slices.Sort(cut.drained)
	assert.Equal(t, []int32{1, 3}, cut.drained)
	assert.Equal(t, []int32{1, 3}, cut.ended)
	assert.Equal(t, []finishedChunk{{partition: 1, lower: 0}, {partition: 3, lower: 0, forced: true}}, cut.chunks)
	assert.Equal(t, batch, cutBatch(batch, 4), "a fully visited poll is kept")
}

// Newest-first reads upward within a chunk, so the first parse errors read
// can all lie below the cursor. The details keep the highest offsets.
func TestSearchScan_NewestFirstKeepsTheParseErrorDetailsAboveTheCursor(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 56}}
	sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
	var recs []*kgo.Record
	for off := range int64(56) {
		v := "bad"
		switch off {
		case 50:
			v = "a"
		case 55:
			v = "b"
		}
		recs = append(recs, rec(0, off, off, v))
	}
	visitAll(sc, recordBatch{chunks: []finishedChunk{{partition: 0, lower: 0}}}, recs...)

	pg := sc.page(ranges, 1)

	assert.Equal(t, []int64{55}, messageOffsets(pg.messages))
	assert.Equal(t, map[int32]int64{0: 51}, pg.next)
	assert.Equal(t, 4, pg.parseErrors)
	var offs []int64
	for _, pe := range pg.parseErrorOffsets {
		offs = append(offs, pe.Offset)
	}
	assert.Equal(t, []int64{51, 52, 53, 54}, offs)
}

// Once every partition is done or holds a full page, reading on cannot
// change the page, so the call ends even without stop_on_limit.
func TestSearchScan_PageFull(t *testing.T) {
	t.Parallel()
	ranges := map[int32]PartitionRange{0: {Start: 0, End: 10}, 1: {Start: 0, End: 10}}

	t.Run(string(DirOldestFirst), func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirOldestFirst)
		visitAll(sc, recordBatch{}, rec(0, 0, 1, "a"), rec(0, 1, 2, "b"), rec(1, 0, 3, "c"))
		assert.False(t, sc.pageFull(2), "partition 1 can still add a match")
		visitAll(sc, recordBatch{drained: []int32{1}, ended: []int32{1}}, rec(1, 9, 4, "bad"))
		assert.True(t, sc.pageFull(2))
	})

	t.Run(string(DirNewestFirst), func(t *testing.T) {
		t.Parallel()
		sc := newSearchScan(errOnMatcher{}, recordDecoder{}, ranges, DirNewestFirst)
		visitAll(sc, recordBatch{chunks: []finishedChunk{{partition: 0, lower: 5}}}, rec(0, 8, 1, "a"), rec(0, 9, 2, "b"))
		visitAll(sc, recordBatch{}, rec(1, 8, 1, "a"), rec(1, 9, 2, "b"))
		assert.False(t, sc.pageFull(2), "the matches of partition 1 are not confirmed yet")
		visitAll(sc, recordBatch{chunks: []finishedChunk{{partition: 1, lower: 5}}})
		assert.True(t, sc.pageFull(2))
	})
}
