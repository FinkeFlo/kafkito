// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

func mustEncodeCursor(t *testing.T, dir kafkapkg.CursorDirection, parts map[int32]int64) string {
	t.Helper()
	enc, err := kafkapkg.EncodeCursor(kafkapkg.Cursor{Direction: dir, Partitions: parts})
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}
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
				if o.Partition != -1 || o.Limit != 50 || o.From != kafkapkg.FromEnd {
					t.Fatalf("defaults wrong: %+v", o)
				}
			},
		},
		{
			name: "from=start with explicit limit",
			raw:  "from=start&limit=10&partition=2",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				if o.Partition != 2 || o.Limit != 10 || o.From != kafkapkg.FromStart {
					t.Fatalf("got %+v", o)
				}
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
				if o.From != kafkapkg.FromOffset || o.Offset != 12345 {
					t.Fatalf("got %+v", o)
				}
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
				if o.From != kafkapkg.FromEnd {
					t.Fatalf("From = %v, want FromEnd", o.From)
				}
				if o.FromTSMs != 1000 || o.ToTSMs != 2000 {
					t.Fatalf("ts bounds = (%d,%d)", o.FromTSMs, o.ToTSMs)
				}
			},
		},
		{
			name: "from=timestamp",
			raw:  "from=timestamp&from_ts_ms=1000",
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				if o.From != kafkapkg.FromTimestamp || o.FromTSMs != 1000 {
					t.Fatalf("got %+v", o)
				}
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
				if o.From != kafkapkg.FromOffset {
					t.Fatalf("From = %v", o.From)
				}
				if o.PartitionOffsets[0] != 42 || o.PartitionOffsets[1] != 99 {
					t.Fatalf("partition_offsets = %+v", o.PartitionOffsets)
				}
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
				if o.From != kafkapkg.FromEnd {
					t.Fatalf("From = %v, want FromEnd", o.From)
				}
				if o.CursorUpperBounds[0] != 100 || o.CursorUpperBounds[1] != 200 {
					t.Fatalf("upper bounds = %+v", o.CursorUpperBounds)
				}
			},
		},
		{
			name: "forward cursor sets PartitionOffsets",
			raw:  "cursor=" + mustEncodeCursor(t, kafkapkg.CursorForward, map[int32]int64{0: 50}),
			check: func(t *testing.T, o kafkapkg.ConsumeOptions) {
				if o.From != kafkapkg.FromOffset {
					t.Fatalf("From = %v, want FromOffset", o.From)
				}
				if o.PartitionOffsets[0] != 50 {
					t.Fatalf("partition_offsets = %+v", o.PartitionOffsets)
				}
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
			if err != nil {
				t.Fatalf("setup: %v", err)
			}
			opts, err := parseConsumeQuery(vals)
			if tc.wantErr != "" {
				var pe *paramError
				if !errors.As(err, &pe) {
					t.Fatalf("expected *paramError, got %T (%v)", err, err)
				}
				if !strings.Contains(pe.Error(), tc.wantErr) {
					t.Fatalf("err = %q, want substring %q", pe.Error(), tc.wantErr)
				}
				if pe.status != 400 {
					t.Fatalf("status = %d, want 400", pe.status)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
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
				if o.Partition != -1 {
					t.Fatalf("partition default = %d, want -1", o.Partition)
				}
				if !o.StopOnLimit {
					t.Fatalf("StopOnLimit default = false, want true")
				}
			},
		},
		{
			name: "full payload",
			body: `{"partition":3,"limit":100,"budget":50000,"direction":"forward","stop_on_limit":false,"mode":"jsonpath","path":"$.amount","op":"gt","value":"100","zones":["value"],"from_ts_ms":1000,"to_ts_ms":2000,"cursors":{"0":42,"1":99}}`,
			check: func(t *testing.T, o kafkapkg.SearchOptions) {
				if o.Partition != 3 || o.Limit != 100 || o.Budget != 50000 {
					t.Fatalf("scalars wrong: %+v", o)
				}
				if o.Direction != kafkapkg.SearchDirection("forward") {
					t.Fatalf("direction = %q", o.Direction)
				}
				if o.StopOnLimit {
					t.Fatalf("StopOnLimit should be false")
				}
				if o.Mode != kafkapkg.SearchMode("jsonpath") || o.Path != "$.amount" || o.Op != kafkapkg.SearchOp("gt") {
					t.Fatalf("filter wrong: %+v", o)
				}
				if len(o.Zones) != 1 || o.Zones[0] != kafkapkg.SearchZone("value") {
					t.Fatalf("zones = %v", o.Zones)
				}
				if o.Cursors[0] != 42 || o.Cursors[1] != 99 {
					t.Fatalf("cursors = %v", o.Cursors)
				}
				if o.FromTS != 1000 || o.ToTS != 2000 {
					t.Fatalf("ts wrong: %+v", o)
				}
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
				if !o.StopOnLimit {
					t.Fatalf("StopOnLimit should be true")
				}
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
				if !errors.As(err, &pe) {
					t.Fatalf("expected *paramError, got %T (%v)", err, err)
				}
				if !strings.Contains(pe.Error(), tc.wantErr) {
					t.Fatalf("err = %q, want substring %q", pe.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			tc.check(t, opts)
		})
	}
}

func TestParseSampleQueryDefaults(t *testing.T) {
	t.Parallel()
	opts, err := parseSampleQuery(url.Values{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Limit != 5 {
		t.Errorf("default Limit = %d, want 5", opts.Limit)
	}
	if opts.Partition != -1 {
		t.Errorf("default Partition = %d, want -1", opts.Partition)
	}
	if opts.From != kafkapkg.FromEnd {
		t.Errorf("default From = %v, want FromEnd", opts.From)
	}
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
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			opts, err := parseSampleQuery(q)
			if err != nil {
				t.Fatalf("parseSampleQuery: %v", err)
			}
			if opts.Limit != tc.wantLimit {
				t.Errorf("Limit = %d, want %d", opts.Limit, tc.wantLimit)
			}
		})
	}
}

func TestWriteParamError(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	if !writeParamError(rec, badParam("nope")) {
		t.Fatal("writeParamError should handle *paramError")
	}
	if rec.Code != 400 {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"nope"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	if writeParamError(rec2, errors.New("plain")) {
		t.Fatal("writeParamError should not handle plain error")
	}
}
