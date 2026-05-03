// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

func mustEncodeCursor(t *testing.T, dir kafkapkg.CursorDirection, parts map[int32]int64) string {
	t.Helper()
	enc, err := kafkapkg.EncodeCursor(kafkapkg.Cursor{Direction: dir, Partitions: parts})
	require.NoError(t, err, "EncodeCursor")
	return enc
}

func TestParseConsumeQuery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		wantErr string
		check   func(t *testing.T, opts kafkapkg.ConsumeOptions)
	}{
		{
			name: "defaults",
			raw:  "",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, int32(-1), o.Partition)
				assert.Equal(t, 50, o.Limit)
				assert.Equal(t, kafkapkg.FromEnd, o.From)
			},
		},
		{
			name: "from=start with explicit limit",
			raw:  "from=start&limit=10&partition=2",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, int32(2), o.Partition)
				assert.Equal(t, 10, o.Limit)
				assert.Equal(t, kafkapkg.FromStart, o.From)
			},
		},
		{
			name:    "from=offset requires offset",
			raw:     "from=offset",
			wantErr: "invalid offset",
		},
		{
			name: "from=offset with valid offset",
			raw:  "from=offset&offset=12345",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromOffset, o.From)
				assert.Equal(t, int64(12345), o.Offset)
			},
		},
		{name: "invalid partition", raw: "partition=abc", wantErr: "invalid partition"},
		{name: "invalid limit (zero)", raw: "limit=0", wantErr: "invalid limit"},
		{name: "invalid limit (negative)", raw: "limit=-3", wantErr: "invalid limit"},
		{name: "invalid limit (text)", raw: "limit=foo", wantErr: "invalid limit"},
		{name: "invalid from", raw: "from=middle", wantErr: "invalid from"},
		{name: "invalid offset value", raw: "from=offset&offset=xx", wantErr: "invalid offset"},
		{
			name: "from=end with time bounds clamps without changing mode",
			raw:  "from=end&from_ts_ms=1000&to_ts_ms=2000",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromEnd, o.From)
				assert.Equal(t, int64(1000), o.FromTSMs)
				assert.Equal(t, int64(2000), o.ToTSMs)
			},
		},
		{
			name: "from=timestamp",
			raw:  "from=timestamp&from_ts_ms=1000",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromTimestamp, o.From)
				assert.Equal(t, int64(1000), o.FromTSMs)
			},
		},
		{name: "negative from_ts_ms", raw: "from_ts_ms=-1", wantErr: "invalid from_ts_ms"},
		{name: "non-numeric from_ts_ms", raw: "from_ts_ms=abc", wantErr: "invalid from_ts_ms"},
		{name: "negative to_ts_ms", raw: "to_ts_ms=-2", wantErr: "invalid to_ts_ms"},
		{name: "to before from", raw: "from_ts_ms=2000&to_ts_ms=1000", wantErr: "to_ts_ms must be >= from_ts_ms"},
		{
			name: "from=offset with partition_offsets",
			raw:  "from=offset&partition_offsets=0:42,1:99",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromOffset, o.From)
				assert.Equal(t, int64(42), o.PartitionOffsets[0])
				assert.Equal(t, int64(99), o.PartitionOffsets[1])
			},
		},
		{
			name:    "partition_offsets requires from=offset",
			raw:     "partition_offsets=0:42",
			wantErr: "partition_offsets requires from=offset",
		},
		{
			name:    "partition_offsets bad pair",
			raw:     "from=offset&partition_offsets=foo",
			wantErr: "invalid partition_offsets",
		},
		{
			name:    "partition_offsets duplicate partition",
			raw:     "from=offset&partition_offsets=0:1,0:2",
			wantErr: "duplicate partition",
		},
		{
			name:    "partition_offsets negative offset",
			raw:     "from=offset&partition_offsets=0:-1",
			wantErr: "negative offset",
		},
		{
			name: "backward cursor sets CursorUpperBounds",
			raw:  "cursor=" + mustEncodeCursor(t, kafkapkg.CursorBackward, map[int32]int64{0: 100, 1: 200}),
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromEnd, o.From)
				assert.Equal(t, int64(100), o.CursorUpperBounds[0])
				assert.Equal(t, int64(200), o.CursorUpperBounds[1])
			},
		},
		{
			name: "forward cursor sets PartitionOffsets",
			raw:  "cursor=" + mustEncodeCursor(t, kafkapkg.CursorForward, map[int32]int64{0: 50}),
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				assert.Equal(t, kafkapkg.FromOffset, o.From)
				assert.Equal(t, int64(50), o.PartitionOffsets[0])
			},
		},
		{
			name:    "backward cursor conflicts with from=start",
			raw:     "from=start&cursor=" + mustEncodeCursor(t, kafkapkg.CursorBackward, map[int32]int64{0: 1}),
			wantErr: "cursor direction backward conflicts",
		},
		{
			name:    "forward cursor conflicts with from=end",
			raw:     "from=end&cursor=" + mustEncodeCursor(t, kafkapkg.CursorForward, map[int32]int64{0: 1}),
			wantErr: "cursor direction forward conflicts",
		},
		{name: "garbage cursor", raw: "cursor=!!!", wantErr: "invalid cursor"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			vals, err := url.ParseQuery(tc.raw)
			require.NoError(t, err, "ParseQuery setup")

			opts, err := parseConsumeQuery(vals)

			if tc.wantErr != "" {
				var pe *paramError
				require.ErrorAs(t, err, &pe)
				assert.Contains(t, pe.Error(), tc.wantErr)
				assert.Equal(t, 400, pe.status)
				return
			}
			require.NoError(t, err)
			tc.check(t, opts)
		})
	}
}

func TestParseSearchBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantErr string
		check   func(t *testing.T, opts kafkapkg.SearchOptions)
	}{
		{
			name: "empty body uses defaults",
			body: "",
			check: func(t *testing.T, o kafkapkg.SearchOptions) {
				assert.Equal(t, int32(-1), o.Partition)
				assert.True(t, o.StopOnLimit, "StopOnLimit default")
			},
		},
		{
			name: "full payload",
			body: `{"partition":3,"limit":100,"budget":50000,"direction":"forward","stop_on_limit":false,"mode":"jsonpath","path":"$.amount","op":"gt","value":"100","zones":["value"],"from_ts_ms":1000,"to_ts_ms":2000,"cursors":{"0":42,"1":99}}`,
			check: func(t *testing.T, o kafkapkg.SearchOptions) {
				assert.Equal(t, int32(3), o.Partition)
				assert.Equal(t, 100, o.Limit)
				assert.Equal(t, 50000, o.Budget)
				assert.Equal(t, kafkapkg.SearchDirection("forward"), o.Direction)
				assert.False(t, o.StopOnLimit)
				assert.Equal(t, kafkapkg.SearchMode("jsonpath"), o.Mode)
				assert.Equal(t, "$.amount", o.Path)
				assert.Equal(t, kafkapkg.SearchOp("gt"), o.Op)
				assert.Equal(t, []kafkapkg.SearchZone{kafkapkg.SearchZone("value")}, o.Zones)
				assert.Equal(t, int64(42), o.Cursors[0])
				assert.Equal(t, int64(99), o.Cursors[1])
				assert.Equal(t, int64(1000), o.FromTS)
				assert.Equal(t, int64(2000), o.ToTS)
			},
		},
		{
			name:    "invalid json",
			body:    `{not-json`,
			wantErr: "invalid json body",
		},
		{
			name:    "invalid cursor key",
			body:    `{"cursors":{"x":1}}`,
			wantErr: "invalid partition key in cursors",
		},
		{
			name: "stop_on_limit explicit true",
			body: `{"stop_on_limit":true}`,
			check: func(t *testing.T, o kafkapkg.SearchOptions) {
				assert.True(t, o.StopOnLimit)
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest("POST", "/x", strings.NewReader(tc.body))

			opts, err := parseSearchBody(req)

			if tc.wantErr != "" {
				var pe *paramError
				require.ErrorAs(t, err, &pe)
				assert.Contains(t, pe.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			tc.check(t, opts)
		})
	}
}

func TestParseSampleQueryDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseSampleQuery(url.Values{})

	require.NoError(t, err)
	assert.Equal(t, 5, opts.Limit, "default Limit")
	assert.Equal(t, int32(-1), opts.Partition, "default Partition")
	assert.Equal(t, kafkapkg.FromEnd, opts.From, "default From")
}

func TestParseSampleQueryCapsN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		query     string
		wantLimit int
	}{
		{"n=100 caps to 25", "n=100", 25},
		{"n=25 stays 25", "n=25", 25},
		{"n=5 stays 5", "n=5", 5},
		{"n=1 stays 1", "n=1", 1},
		{"n=0 raises to 1", "n=0", 1},
		{"n=-3 raises to 1", "n=-3", 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			q, err := url.ParseQuery(tc.query)
			require.NoError(t, err, "ParseQuery setup")

			opts, err := parseSampleQuery(q)

			require.NoError(t, err)
			assert.Equal(t, tc.wantLimit, opts.Limit)
		})
	}
}

func TestWriteParamError(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	require.True(t, writeParamError(rec, badParam("nope")), "writeParamError should handle *paramError")
	assert.Equal(t, 400, rec.Code)
	assert.Contains(t, rec.Body.String(), `"nope"`)

	rec2 := httptest.NewRecorder()
	assert.False(t,
		writeParamError(rec2, errors.New("plain")),
		"writeParamError should not handle plain error")
}
