// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func backwardPageOffsets(w pageWindow) []Message {
	page := make([]Message, 0, int(w.stop-w.begin))
	for off := w.stop - 1; off >= w.begin; off-- {
		page = append(page, Message{Partition: 0, Offset: off})
	}
	return page
}

// collectedByPartition groups a slice of messages by partition, mirroring the
// pre-truncation `collected` map that ConsumeMessages passes to buildNextCursor.
func collectedByPartition(msgs []Message) map[int32][]Message {
	out := make(map[int32][]Message)
	for _, m := range msgs {
		out[m.Partition] = append(out[m.Partition], m)
	}
	return out
}

// TestBuildWindowsFromEndTimeBounds verifies that a from=end browse honors
// from_ts_ms / to_ts_ms by clamping the per-partition [begin, stop) range
// before fairShare and the e-tail tail computation. This is the core
// invariant of the time-windowed messages view.
func TestBuildWindowsFromEndTimeBounds(t *testing.T) {
	t.Parallel()
	parts := []int32{0, 1, 2}
	startMap := map[int32]int64{0: 0, 1: 0, 2: 0}
	endMap := map[int32]int64{0: 1000, 1: 1000, 2: 1000}

	t.Run("upper bound clamps stop", func(t *testing.T) {
		t.Parallel()
		opts := ConsumeOptions{
			From:   FromEnd,
			Limit:  30,
			ToTSMs: 1, // any non-zero
		}
		toOff := map[int32]int64{0: 500, 1: 500, 2: 500}
		windows, err := buildWindows(parts, opts, startMap, endMap, nil, toOff)
		require.NoError(t, err)
		require.Len(t, windows, 3)
		for _, w := range windows {
			require.Equal(t, int64(500), w.stop, "stop must be clamped to toOff")
			require.GreaterOrEqual(t, w.begin, int64(0))
			require.Less(t, w.begin, w.stop)
		}
	})

	t.Run("lower bound clamps begin", func(t *testing.T) {
		t.Parallel()
		opts := ConsumeOptions{
			From:     FromEnd,
			Limit:    30,
			FromTSMs: 1,
		}
		fromOff := map[int32]int64{0: 990, 1: 990, 2: 990}
		windows, err := buildWindows(parts, opts, startMap, endMap, fromOff, nil)
		require.NoError(t, err)
		for _, w := range windows {
			require.GreaterOrEqual(t, w.begin, int64(990), "begin must be lifted to fromOff")
			require.Equal(t, int64(1000), w.stop)
		}
	})

	t.Run("partitions outside window drop out of fairShare denominator", func(t *testing.T) {
		t.Parallel()
		opts := ConsumeOptions{
			From:     FromEnd,
			Limit:    30,
			FromTSMs: 1,
			ToTSMs:   1,
		}
		// Partition 2 has no records inside the window (toOff <= fromOff).
		fromOff := map[int32]int64{0: 100, 1: 100, 2: 600}
		toOff := map[int32]int64{0: 200, 1: 200, 2: 500}
		windows, err := buildWindows(parts, opts, startMap, endMap, fromOff, toOff)
		require.NoError(t, err)
		// Partition 2's window collapses (toOff < fromOff would normally
		// keep stop < begin — drop it).
		_, p2 := windows[2]
		// Either dropped, or empty range — both acceptable; explicitly the
		// implementation drops collapsed windows.
		require.False(t, p2 || (windows[2].stop > windows[2].begin), "partition 2 window must be empty/dropped")
		// Each surviving partition gets ceil(30/2)+buffer share (active=2).
		// Window range is [100, 200) = 100 records, smaller than the fair
		// share, so begin = max(100, 200-share) ≈ 100 (full range used).
		for p := range windows {
			require.Equal(t, int64(200), windows[p].stop)
			require.GreaterOrEqual(t, windows[p].begin, int64(100))
		}
	})

	t.Run("cursor upper bound layers on top of toOff", func(t *testing.T) {
		t.Parallel()
		opts := ConsumeOptions{
			From:              FromEnd,
			Limit:             30,
			ToTSMs:            1,
			CursorUpperBounds: map[int32]int64{0: 400, 1: 400, 2: 400},
		}
		toOff := map[int32]int64{0: 500, 1: 500, 2: 500}
		windows, err := buildWindows(parts, opts, startMap, endMap, nil, toOff)
		require.NoError(t, err)
		for _, w := range windows {
			require.Equal(t, int64(400), w.stop, "cursor upper bound (400) must win over toOff (500)")
		}
	})
}

// TestBuildWindowsFromStartTimeBounds covers the symmetrical FromStart
// branch: time bounds clamp [begin, stop) but the entire window is read
// (no fairShare tail trimming on the forward path).
func TestBuildWindowsFromStartTimeBounds(t *testing.T) {
	t.Parallel()
	parts := []int32{0, 1}
	startMap := map[int32]int64{0: 0, 1: 0}
	endMap := map[int32]int64{0: 1000, 1: 1000}
	opts := ConsumeOptions{
		From:     FromStart,
		Limit:    30,
		FromTSMs: 1,
		ToTSMs:   1,
	}
	fromOff := map[int32]int64{0: 100, 1: 200}
	toOff := map[int32]int64{0: 500, 1: 700}
	windows, err := buildWindows(parts, opts, startMap, endMap, fromOff, toOff)
	require.NoError(t, err)
	require.Equal(t, int64(100), windows[0].begin)
	require.Equal(t, int64(500), windows[0].stop)
	require.Equal(t, int64(200), windows[1].begin)
	require.Equal(t, int64(700), windows[1].stop)
}

// TestBuildNextCursorBackwardUsesQueryLowerBound verifies the backward path
// compares against the full query lower bound, not the narrowed per-page
// fetch begin.
func TestBuildNextCursorBackwardUsesQueryLowerBound(t *testing.T) {
	t.Parallel()
	t.Run("page reaches query lower bound: no next page", func(t *testing.T) {
		t.Parallel()
		windows := map[int32]pageWindow{
			0: {begin: 100, stop: 200, trueBegin: 100},
		}
		page := []Message{{Partition: 0, Offset: 100}, {Partition: 0, Offset: 150}}
		cursor, hasMore := buildNextCursor(CursorBackward, windows, page, collectedByPartition(page), nil)
		require.False(t, hasMore)
		require.Nil(t, cursor)
	})
	t.Run("page reaches narrowed begin but not query lower bound: next page", func(t *testing.T) {
		t.Parallel()
		windows := map[int32]pageWindow{
			0: {begin: 100, stop: 200, trueBegin: 0},
		}
		page := []Message{{Partition: 0, Offset: 100}, {Partition: 0, Offset: 150}}
		cursor, hasMore := buildNextCursor(CursorBackward, windows, page, collectedByPartition(page), nil)
		require.True(t, hasMore)
		require.NotNil(t, cursor)
		require.Equal(t, int64(100), cursor.Partitions[0])
	})
}

// TestBuildNextCursorForwardWithClampedWindow verifies the forward
// equivalent: hasMore checks against w.stop only, not an unclamped end
// offset.
func TestBuildNextCursorForwardWithClampedWindow(t *testing.T) {
	t.Parallel()
	t.Run("page reaches clamped stop: no next page", func(t *testing.T) {
		t.Parallel()
		windows := map[int32]pageWindow{
			0: {begin: 100, stop: 200, trueBegin: 100},
		}
		page := []Message{{Partition: 0, Offset: 199}}
		cursor, hasMore := buildNextCursor(CursorForward, windows, page, nil, nil)
		require.False(t, hasMore)
		require.Nil(t, cursor)
	})
	t.Run("page does not reach clamped stop: next page", func(t *testing.T) {
		t.Parallel()
		windows := map[int32]pageWindow{
			0: {begin: 100, stop: 500, trueBegin: 100},
		}
		page := []Message{{Partition: 0, Offset: 150}}
		cursor, hasMore := buildNextCursor(CursorForward, windows, page, nil, nil)
		require.True(t, hasMore)
		require.NotNil(t, cursor)
		require.Equal(t, int64(151), cursor.Partitions[0])
	})
}

func TestBuildWindowsFromEndPaginatesToTrueStart(t *testing.T) {
	t.Parallel()
	parts := []int32{0}
	startMap := map[int32]int64{0: 0}
	endMap := map[int32]int64{0: 10}
	opts := ConsumeOptions{
		From:  FromEnd,
		Limit: 3,
	}

	var seen []int64
	for {
		windows, err := buildWindows(parts, opts, startMap, endMap, nil, nil)
		require.NoError(t, err)
		require.Len(t, windows, 1)

		w := windows[0]
		page := backwardPageOffsets(w)
		require.NotEmpty(t, page)
		for _, m := range page {
			seen = append(seen, m.Offset)
		}

		cursor, hasMore := buildNextCursor(CursorBackward, windows, page, collectedByPartition(page), nil)
		if !hasMore {
			require.Nil(t, cursor)
			break
		}
		require.NotNil(t, cursor)
		opts.CursorUpperBounds = cursor.Partitions
	}

	require.Equal(t, []int64{9, 8, 7, 6, 5, 4, 3, 2, 1, 0}, seen)
}

// TestBuildNextCursorBackwardMultiPartitionNoLoss reproduces the from=end,
// partition=-1 "load more" data-loss case: when one partition's records are
// all dropped by the per-page limit truncation (because a busier partition
// carries newer timestamps), the older partition must keep its window boundary
// so a later page still fetches it, instead of skipping the dropped window.
func TestBuildNextCursorBackwardMultiPartitionNoLoss(t *testing.T) {
	t.Parallel()

	parts := []int32{0, 1}
	startMap := map[int32]int64{0: 0, 1: 0}
	endMap := map[int32]int64{0: 10, 1: 10}

	// Partition 0 carries newer timestamps than partition 1, so early pages
	// fill entirely from partition 0 and truncate partition 1 away.
	ts := func(p int32, off int64) int64 {
		if p == 0 {
			return 100_000 + off
		}
		return off
	}

	opts := ConsumeOptions{From: FromEnd, Limit: 3}
	seen := map[int32]map[int64]int{0: {}, 1: {}}

	for page := 0; page < 100; page++ {
		windows, err := buildWindows(parts, opts, startMap, endMap, nil, nil)
		require.NoError(t, err)
		if len(windows) == 0 {
			break
		}

		// Simulate the fetch/merge/truncate pipeline of ConsumeMessages.
		collected := make(map[int32][]Message, len(windows))
		var merged []Message
		for p, w := range windows {
			for off := w.begin; off < w.stop; off++ {
				m := Message{Partition: p, Offset: off, Timestamp: ts(p, off)}
				collected[p] = append(collected[p], m)
				merged = append(merged, m)
			}
		}
		sort.Slice(merged, func(i, j int) bool {
			if merged[i].Timestamp != merged[j].Timestamp {
				return merged[i].Timestamp > merged[j].Timestamp
			}
			if merged[i].Partition != merged[j].Partition {
				return merged[i].Partition < merged[j].Partition
			}
			return merged[i].Offset > merged[j].Offset
		})
		if len(merged) > opts.Limit {
			merged = merged[:opts.Limit]
		}
		for _, m := range merged {
			seen[m.Partition][m.Offset]++
		}

		cursor, hasMore := buildNextCursor(CursorBackward, windows, merged, collected, opts.CursorUpperBounds)
		if !hasMore {
			require.Nil(t, cursor)
			break
		}
		require.NotNil(t, cursor)
		opts.CursorUpperBounds = cursor.Partitions
	}

	for p := int32(0); p <= 1; p++ {
		for off := startMap[p]; off < endMap[p]; off++ {
			require.Equal(t, 1, seen[p][off],
				"partition %d offset %d should be delivered exactly once", p, off)
		}
	}
}
