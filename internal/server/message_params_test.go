// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// These tests cover the mapping from the generated (already validated)
// parameters to the kafka options: defaults, clamping and the cross-field
// rules the spec cannot express. The spec-enforced rules and the error
// texts on the wire are covered by TestTopicMessageOps_Validation.

func ptr[T any](v T) *T { return &v }

func mustEncodeCursor(t *testing.T, dir kafkapkg.CursorDirection, parts map[int32]int64) string {
	t.Helper()
	enc, err := kafkapkg.EncodeCursor(kafkapkg.Cursor{Direction: dir, Partitions: parts})
	require.NoError(t, err, "EncodeCursor")
	return enc
}

// assertBadRequest checks err is a 400 apiError containing want.
func assertBadRequest(t *testing.T, err error, want string) {
	t.Helper()
	var ae *apiError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, 400, ae.Status)
	assert.Contains(t, ae.Message, want)
}

func TestConsumeOptions(t *testing.T) {
	t.Parallel()
	from := func(s string) *gen.ConsumeMessagesParamsFrom { v := gen.ConsumeMessagesParamsFrom(s); return &v }
	tests := []struct {
		name    string
		params  gen.ConsumeMessagesParams
		wantErr string
		check   func(t *testing.T, opts kafkapkg.ConsumeOptions)
	}{
		{
			name: "defaults",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, int32(-1), o.Partition)
				assert.Equal(t, 50, o.Limit)
				assert.Equal(t, kafkapkg.FromEnd, o.From)
				assert.Equal(t, "6s", o.Timeout.String())
			},
		},
		{
			name:   "from=start with explicit limit",
			params: gen.ConsumeMessagesParams{From: from("start"), Limit: ptr(10), Partition: ptr(int32(2))},
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, int32(2), o.Partition)
				assert.Equal(t, 10, o.Limit)
				assert.Equal(t, kafkapkg.FromStart, o.From)
			},
		},
		{name: "from=offset requires offset", params: gen.ConsumeMessagesParams{From: from("offset")}, wantErr: "invalid offset"},
		{
			name:   "from=offset with valid offset",
			params: gen.ConsumeMessagesParams{From: from("offset"), Offset: ptr(int64(12345))},
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromOffset, o.From)
				assert.Equal(t, int64(12345), o.Offset)
			},
		},
		{
			name:   "limit 1 is kept",
			params: gen.ConsumeMessagesParams{Limit: ptr(1)},
			check:  func(t *testing.T, o kafkapkg.ConsumeOptions) { assert.Equal(t, 1, o.Limit) },
		},
		{
			name:   "limit at cap is kept",
			params: gen.ConsumeMessagesParams{Limit: ptr(maxConsumeLimit)},
			check:  func(t *testing.T, o kafkapkg.ConsumeOptions) { assert.Equal(t, maxConsumeLimit, o.Limit) },
		},
		{
			name:   "limit above cap is clamped to maxConsumeLimit",
			params: gen.ConsumeMessagesParams{Limit: ptr(maxConsumeLimit + 1)},
			check:  func(t *testing.T, o kafkapkg.ConsumeOptions) { assert.Equal(t, maxConsumeLimit, o.Limit) },
		},
		{
			name:   "from=end with time bounds clamps without changing mode",
			params: gen.ConsumeMessagesParams{From: from("end"), FromTsMs: ptr(int64(1000)), ToTsMs: ptr(int64(2000))},
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromEnd, o.From)
				assert.Equal(t, int64(1000), o.FromTSMs)
				assert.Equal(t, int64(2000), o.ToTSMs)
			},
		},
		{
			name:   "from=timestamp",
			params: gen.ConsumeMessagesParams{From: from("timestamp"), FromTsMs: ptr(int64(1000))},
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromTimestamp, o.From)
				assert.Equal(t, int64(1000), o.FromTSMs)
			},
		},
		{name: "to before from", params: gen.ConsumeMessagesParams{FromTsMs: ptr(int64(2000)), ToTsMs: ptr(int64(1000))}, wantErr: "to_ts_ms must be >= from_ts_ms"},
		{
			name:   "to equal to from",
			params: gen.ConsumeMessagesParams{FromTsMs: ptr(int64(2000)), ToTsMs: ptr(int64(2000))},
			check:  func(t *testing.T, o kafkapkg.ConsumeOptions) { assert.Equal(t, int64(2000), o.ToTSMs) },
		},
		{
			name:   "from=offset with partition_offsets",
			params: gen.ConsumeMessagesParams{From: from("offset"), PartitionOffsets: ptr("0:42,1:99")},
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromOffset, o.From)
				assert.Equal(t, int64(42), o.PartitionOffsets[0])
				assert.Equal(t, int64(99), o.PartitionOffsets[1])
			},
		},
		{
			name:   "empty partition_offsets is ignored",
			params: gen.ConsumeMessagesParams{PartitionOffsets: ptr("")},
			check:  func(t *testing.T, o kafkapkg.ConsumeOptions) { assert.Nil(t, o.PartitionOffsets) },
		},
		{name: "partition_offsets requires from=offset", params: gen.ConsumeMessagesParams{PartitionOffsets: ptr("0:42")}, wantErr: "partition_offsets requires from=offset"},
		{name: "partition_offsets bad pair", params: gen.ConsumeMessagesParams{From: from("offset"), PartitionOffsets: ptr("foo")}, wantErr: "invalid partition_offsets"},
		{name: "partition_offsets duplicate partition", params: gen.ConsumeMessagesParams{From: from("offset"), PartitionOffsets: ptr("0:1,0:2")}, wantErr: "duplicate partition"},
		{name: "partition_offsets negative offset", params: gen.ConsumeMessagesParams{From: from("offset"), PartitionOffsets: ptr("0:-1")}, wantErr: "negative offset"},
		{
			name:   "backward cursor sets CursorUpperBounds",
			params: gen.ConsumeMessagesParams{Cursor: ptr(mustEncodeCursor(t, kafkapkg.CursorBackward, map[int32]int64{0: 100, 1: 200}))},
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromEnd, o.From)
				assert.Equal(t, int64(100), o.CursorUpperBounds[0])
				assert.Equal(t, int64(200), o.CursorUpperBounds[1])
			},
		},
		{
			name:   "forward cursor sets PartitionOffsets",
			params: gen.ConsumeMessagesParams{Cursor: ptr(mustEncodeCursor(t, kafkapkg.CursorForward, map[int32]int64{0: 50}))},
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromOffset, o.From)
				assert.Equal(t, int64(50), o.PartitionOffsets[0])
			},
		},
		{
			name:   "backward cursor with explicit from=end",
			params: gen.ConsumeMessagesParams{From: from("end"), Cursor: ptr(mustEncodeCursor(t, kafkapkg.CursorBackward, map[int32]int64{0: 1}))},
			check:  func(t *testing.T, o kafkapkg.ConsumeOptions) { assert.Equal(t, kafkapkg.FromEnd, o.From) },
		},
		{
			name:    "backward cursor conflicts with from=start",
			params:  gen.ConsumeMessagesParams{From: from("start"), Cursor: ptr(mustEncodeCursor(t, kafkapkg.CursorBackward, map[int32]int64{0: 1}))},
			wantErr: "cursor direction backward conflicts with from=start",
		},
		{
			name:    "forward cursor conflicts with from=end",
			params:  gen.ConsumeMessagesParams{From: from("end"), Cursor: ptr(mustEncodeCursor(t, kafkapkg.CursorForward, map[int32]int64{0: 1}))},
			wantErr: "cursor direction forward conflicts with from=end",
		},
		{name: "garbage cursor", params: gen.ConsumeMessagesParams{Cursor: ptr("!!!")}, wantErr: "invalid cursor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, err := consumeOptions(tc.params)
			if tc.wantErr != "" {
				assertBadRequest(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			tc.check(t, opts)
		})
	}
}

func TestParsePartitionOffsets_EntryCap(t *testing.T) {
	t.Parallel()
	build := func(n int) string {
		s := ""
		for i := range n {
			if i > 0 {
				s += ","
			}
			s += fmt.Sprintf("%d:0", i)
		}
		return s
	}
	offs, err := parsePartitionOffsets(build(maxPartitionOffsetsEntries))
	require.NoError(t, err)
	assert.Len(t, offs, maxPartitionOffsetsEntries)

	_, err = parsePartitionOffsets(build(maxPartitionOffsetsEntries + 1))
	assert.ErrorContains(t, err, "too many entries (max 1024)")
}

func TestCountOptions(t *testing.T) {
	t.Parallel()

	o, err := countOptions(gen.CountMessagesParams{})
	require.NoError(t, err)
	assert.Equal(t, int32(-1), o.Partition)
	assert.Zero(t, o.FromTSMs)
	assert.Zero(t, o.ToTSMs)
	assert.Equal(t, "6s", o.Timeout.String())

	o, err = countOptions(gen.CountMessagesParams{Partition: ptr(int32(2)), FromTsMs: ptr(int64(1000)), ToTsMs: ptr(int64(2000))})
	require.NoError(t, err)
	assert.Equal(t, int32(2), o.Partition)
	assert.Equal(t, int64(1000), o.FromTSMs)
	assert.Equal(t, int64(2000), o.ToTSMs)

	_, err = countOptions(gen.CountMessagesParams{FromTsMs: ptr(int64(2000)), ToTsMs: ptr(int64(1000))})
	assertBadRequest(t, err, "to_ts_ms must be >= from_ts_ms")
}

func TestTimelineOptions(t *testing.T) {
	t.Parallel()

	o, err := timelineOptions(gen.GetMessageTimelineParams{FromTsMs: 1000, ToTsMs: 2000, SlotMs: 100})
	require.NoError(t, err)
	assert.Equal(t, int32(-1), o.Partition)
	assert.Equal(t, int64(1000), o.FromTSMs)
	assert.Equal(t, int64(2000), o.ToTSMs)
	assert.Equal(t, int64(100), o.SlotMs)
	assert.Equal(t, "20s", o.Timeout.String())

	o, err = timelineOptions(gen.GetMessageTimelineParams{Partition: ptr(int32(3)), FromTsMs: 1, ToTsMs: 2, SlotMs: 1})
	require.NoError(t, err)
	assert.Equal(t, int32(3), o.Partition)

	_, err = timelineOptions(gen.GetMessageTimelineParams{FromTsMs: 2000, ToTsMs: 2000, SlotMs: 1})
	assertBadRequest(t, err, "to_ts_ms must be > from_ts_ms")

	// Exactly MaxTimelineSlots slots is fine, one more is rejected.
	_, err = timelineOptions(gen.GetMessageTimelineParams{FromTsMs: 1, ToTsMs: 1 + kafkapkg.MaxTimelineSlots, SlotMs: 1})
	require.NoError(t, err)
	_, err = timelineOptions(gen.GetMessageTimelineParams{FromTsMs: 1, ToTsMs: 2 + kafkapkg.MaxTimelineSlots, SlotMs: 1})
	assertBadRequest(t, err, fmt.Sprintf("range/slot combination yields %d slots, exceeding the limit of %d", kafkapkg.MaxTimelineSlots+1, kafkapkg.MaxTimelineSlots))
}

func TestSampleOptions(t *testing.T) {
	t.Parallel()

	opts := sampleOptions(gen.SampleMessagesParams{})
	assert.Equal(t, 5, opts.Limit, "default Limit")
	assert.Equal(t, int32(-1), opts.Partition, "default Partition")
	assert.Equal(t, kafkapkg.FromEnd, opts.From, "default From")
	assert.Equal(t, "6s", opts.Timeout.String())

	assert.Equal(t, int32(4), sampleOptions(gen.SampleMessagesParams{Partition: ptr(int32(4))}).Partition)

	for n, want := range map[int]int{100: 25, 26: 25, 25: 25, 5: 5, 1: 1, 0: 1, -3: 1} {
		assert.Equal(t, want, sampleOptions(gen.SampleMessagesParams{N: ptr(n)}).Limit, "n=%d", n)
	}
}

func TestSearchOptions(t *testing.T) {
	t.Parallel()

	for _, body := range []*gen.SearchRequest{nil, {}} {
		o, err := searchOptions(body)
		require.NoError(t, err)
		assert.Equal(t, int32(-1), o.Partition)
		assert.True(t, o.StopOnLimit, "StopOnLimit default")
		assert.Equal(t, "12s", o.Timeout.String())
	}

	dir := gen.SearchRequestDirection("oldest_first")
	mode := gen.SearchRequestMode("jsonpath")
	op := gen.SearchRequestOp("gt")
	zones := []gen.SearchRequestZones{"value", "key"}
	cursors := map[string]int64{"0": 42, "1": 99}
	o, err := searchOptions(&gen.SearchRequest{
		Partition: ptr(int32(3)), Limit: ptr(100), Budget: ptr(50000), Direction: &dir,
		StopOnLimit: ptr(false), Mode: &mode, Path: ptr("$.amount"), Op: &op, Value: ptr("100"),
		Zones: &zones, FromTsMs: ptr(int64(1000)), ToTsMs: ptr(int64(2000)), Cursors: &cursors,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), o.Partition)
	assert.Equal(t, 100, o.Limit)
	assert.Equal(t, 50000, o.Budget)
	assert.Equal(t, kafkapkg.SearchDirection("oldest_first"), o.Direction)
	assert.False(t, o.StopOnLimit)
	assert.Equal(t, kafkapkg.SearchMode("jsonpath"), o.Mode)
	assert.Equal(t, "$.amount", o.Path)
	assert.Equal(t, kafkapkg.SearchOp("gt"), o.Op)
	assert.Equal(t, "100", o.Value)
	assert.Equal(t, []kafkapkg.SearchZone{"value", "key"}, o.Zones)
	assert.Equal(t, map[int32]int64{0: 42, 1: 99}, o.Cursors)
	assert.Equal(t, int64(1000), o.FromTS)
	assert.Equal(t, int64(2000), o.ToTS)

	o, err = searchOptions(&gen.SearchRequest{StopOnLimit: ptr(true)})
	require.NoError(t, err)
	assert.True(t, o.StopOnLimit)

	_, err = searchOptions(&gen.SearchRequest{Cursors: &map[string]int64{"x": 1}})
	assertBadRequest(t, err, `invalid partition key in cursors: "x"`)
}
