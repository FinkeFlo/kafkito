// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func msgsAt(p int32, ts int64, offsets ...int64) []Message {
	out := make([]Message, 0, len(offsets))
	for i, o := range offsets {
		out = append(out, Message{Partition: p, Offset: o, Timestamp: ts + int64(i)})
	}
	return out
}

func TestRefillPlan(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		windows   map[int32]pageWindow
		collected map[int32][]Message
		limit     int
		want      map[int32]pageWindow
	}{
		"complete page needs nothing": {
			windows:   map[int32]pageWindow{0: {begin: 16, stop: 20, trueBegin: 0}},
			collected: map[int32][]Message{0: msgsAt(0, 100, 16, 17, 18, 19)},
			limit:     4,
			want:      map[int32]pageWindow{},
		},
		"window at the start of the range needs nothing": {
			windows:   map[int32]pageWindow{0: {begin: 0, stop: 4, trueBegin: 0}},
			collected: map[int32][]Message{0: msgsAt(0, 100, 0, 2)},
			limit:     4,
			want:      map[int32]pageWindow{},
		},
		"markers in the window widen it by the observed record density": {
			// Every other offset is a transaction marker: 2 records in 4
			// offsets, 2 missing, so 4 more offsets.
			windows:   map[int32]pageWindow{0: {begin: 16, stop: 20, trueBegin: 0}},
			collected: map[int32][]Message{0: msgsAt(0, 100, 16, 18)},
			limit:     4,
			want:      map[int32]pageWindow{0: {begin: 12, stop: 16, trueBegin: 0}},
		},
		"widening stops at the start of the range": {
			windows:   map[int32]pageWindow{0: {begin: 2, stop: 6, trueBegin: 0}},
			collected: map[int32][]Message{0: msgsAt(0, 100, 2, 4)},
			limit:     4,
			want:      map[int32]pageWindow{0: {begin: 0, stop: 2, trueBegin: 0}},
		},
		"window without records is widened past its span": {
			windows:   map[int32]pageWindow{0: {begin: 19, stop: 20, trueBegin: 0}},
			collected: map[int32][]Message{},
			limit:     1,
			want:      map[int32]pageWindow{0: {begin: 17, stop: 19, trueBegin: 0}},
		},
		"full page skips a window whose older records rank below it": {
			windows: map[int32]pageWindow{
				0: {begin: 8, stop: 10, trueBegin: 0},
				1: {begin: 8, stop: 10, trueBegin: 0},
			},
			collected: map[int32][]Message{
				0: msgsAt(0, 100, 8, 9),
				1: msgsAt(1, 200, 8, 9),
			},
			limit: 2,
			want:  map[int32]pageWindow{},
		},
		"full page widens a capped window whose older records still rank inside": {
			windows: map[int32]pageWindow{
				0: {begin: 8, stop: 10, trueBegin: 0},
				1: {begin: 9, stop: 10, trueBegin: 0},
			},
			collected: map[int32][]Message{
				0: msgsAt(0, 100, 8, 9),
				1: msgsAt(1, 300, 9),
			},
			limit: 2,
			want:  map[int32]pageWindow{1: {begin: 8, stop: 9, trueBegin: 0}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, refillPlan(c.windows, c.collected, c.limit))
		})
	}
}

func TestWindowsComplete(t *testing.T) {
	t.Parallel()
	windows := map[int32]pageWindow{0: {begin: 0, stop: 4}}
	gappy := map[int32][]Message{0: msgsAt(0, 100, 0, 2)}
	assert.False(t, windowsComplete(windows, gappy, nil), "short and not ended")
	assert.True(t, windowsComplete(windows, gappy, map[int32]bool{0: true}), "short but read to its end")
	assert.True(t, windowsComplete(windows, map[int32][]Message{0: msgsAt(0, 100, 0, 1, 2, 3)}, nil), "one record per offset")
}

// A chunk ended by the drainAfter fallback drains its partition but does not
// count as read to its end, so a from=end page cut short that way is partial.
func TestReadToEndSkipsForcedDrains(t *testing.T) {
	t.Parallel()
	cursors := map[int32]*scanCursor{
		0: {lower: 0, upper: 4, pos: 0, done: true}, // last offset seen
		1: {lower: 0, upper: 4, pos: 0},             // stalled
	}
	forceDone(cursors)
	drained := exhausted(cursors)
	assert.ElementsMatch(t, []int32{0, 1}, drained)
	assert.Equal(t, []int32{0}, readToEnd(cursors, drained))
}
