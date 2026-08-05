// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestDecodeProducePayload(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		value    string
		encoding string
		want     []byte
		wantNil  bool
		wantErr  string
	}{
		{name: "default encoding text", value: "hello", encoding: "", want: []byte("hello")},
		{name: "explicit text", value: "hello", encoding: "text", want: []byte("hello")},
		{name: "text utf8 multibyte", value: "héllo ✓", encoding: "text", want: []byte("héllo ✓")},
		{name: "empty text is tombstone", value: "", encoding: "text", wantNil: true},
		{name: "empty default encoding is tombstone", value: "", encoding: "", wantNil: true},
		{name: "empty base64 is tombstone", value: "", encoding: "base64", wantNil: true},

		{name: "empty encoding ignores value", value: "", encoding: "empty", want: []byte{}},
		{name: "empty encoding ignores non-empty value", value: "ignored", encoding: "empty", want: []byte{}},

		{name: "base64 std", value: base64.StdEncoding.EncodeToString([]byte{0xde, 0xad, 0xbe, 0xef}), encoding: "base64", want: []byte{0xde, 0xad, 0xbe, 0xef}},
		{name: "base64 std with padding", value: "aGk=", encoding: "base64", want: []byte("hi")},
		{name: "base64 url safe", value: base64.URLEncoding.EncodeToString([]byte{0xfb, 0xff, 0xfe}), encoding: "base64", want: []byte{0xfb, 0xff, 0xfe}},
		{name: "base64 raw std unpadded", value: base64.RawStdEncoding.EncodeToString([]byte("hi")), encoding: "base64", want: []byte("hi")},

		{name: "invalid base64", value: "not base64 at all!!", encoding: "base64", wantErr: "invalid base64"},
		{name: "unsupported encoding", value: "x", encoding: "hex", wantErr: `unsupported encoding "hex"`},
		{name: "unsupported encoding binary", value: "x", encoding: "binary", wantErr: "unsupported encoding"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeProducePayload(tc.value, tc.encoding)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, got, "expected a nil (tombstone) payload")
				return
			}
			require.NotNil(t, got, "expected a non-nil payload")
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDecodeProducePayload_EmptyVsText pins down the exact difference between
// the "empty" and "text" encodings for a zero-length payload: nil (tombstone)
// vs non-nil zero length (verbatim empty payload).
func TestDecodeProducePayload_EmptyVsText(t *testing.T) {
	t.Parallel()

	text, err := decodeProducePayload("", "text")
	require.NoError(t, err)
	assert.Nil(t, text)

	empty, err := decodeProducePayload("", "empty")
	require.NoError(t, err)
	require.NotNil(t, empty)
	assert.Len(t, empty, 0)
}

func TestBuildRecord_PayloadsAndPartition(t *testing.T) {
	t.Parallel()

	part := int32(7)

	cases := []struct {
		name          string
		req           ProduceRequest
		wantKeyNil    bool
		wantValNil    bool
		wantKey       []byte
		wantVal       []byte
		wantPartition int32
	}{
		{
			name:          "text key and value, no partition",
			req:           ProduceRequest{Key: "k", Value: "v"},
			wantKey:       []byte("k"),
			wantVal:       []byte("v"),
			wantPartition: unsetPartition,
		},
		{
			name:          "partition pointer honoured",
			req:           ProduceRequest{Key: "k", Value: "v", Partition: &part},
			wantKey:       []byte("k"),
			wantVal:       []byte("v"),
			wantPartition: 7,
		},
		{
			name:          "tombstone value",
			req:           ProduceRequest{Key: "k", Value: "", ValueEncoding: "text"},
			wantKey:       []byte("k"),
			wantValNil:    true,
			wantPartition: unsetPartition,
		},
		{
			name:          "empty encoding keeps zero-length key and value",
			req:           ProduceRequest{KeyEncoding: "empty", ValueEncoding: "empty"},
			wantKey:       []byte{},
			wantVal:       []byte{},
			wantPartition: unsetPartition,
		},
		{
			name:          "base64 binary value",
			req:           ProduceRequest{Value: "3q2+7w==", ValueEncoding: "base64"},
			wantKeyNil:    true,
			wantVal:       []byte{0xde, 0xad, 0xbe, 0xef},
			wantPartition: unsetPartition,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, err := buildRecord("dst-topic", tc.req)
			require.NoError(t, err)
			require.NotNil(t, rec)
			assert.Equal(t, "dst-topic", rec.Topic)
			assert.Equal(t, tc.wantPartition, rec.Partition)

			if tc.wantKeyNil {
				assert.Nil(t, rec.Key)
			} else {
				require.NotNil(t, rec.Key)
				assert.Equal(t, tc.wantKey, rec.Key)
			}
			if tc.wantValNil {
				assert.Nil(t, rec.Value)
			} else {
				require.NotNil(t, rec.Value)
				assert.Equal(t, tc.wantVal, rec.Value)
			}
		})
	}
}

func TestBuildRecord_PayloadErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		req     ProduceRequest
		wantErr string
	}{
		{name: "bad key encoding", req: ProduceRequest{KeyEncoding: "hex", Key: "ff"}, wantErr: `key: unsupported encoding "hex"`},
		{name: "bad value encoding", req: ProduceRequest{ValueEncoding: "hex", Value: "ff"}, wantErr: `value: unsupported encoding "hex"`},
		{name: "bad key base64", req: ProduceRequest{KeyEncoding: "base64", Key: "!!!!"}, wantErr: "key: invalid base64"},
		{name: "bad value base64", req: ProduceRequest{ValueEncoding: "base64", Value: "!!!!"}, wantErr: "value: invalid base64"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, err := buildRecord("t", tc.req)
			require.Error(t, err)
			assert.Nil(t, rec)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildRecord_Headers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		req     ProduceRequest
		want    map[string][]byte // key -> exact bytes, each expected exactly once
		wantErr string
	}{
		{
			name: "plain utf8 headers",
			req:  ProduceRequest{Headers: map[string]string{"a": "1", "b": "two"}},
			want: map[string][]byte{"a": []byte("1"), "b": []byte("two")},
		},
		{
			name: "headers_b64 only",
			req:  ProduceRequest{HeadersB64: map[string]string{"bin": base64.StdEncoding.EncodeToString([]byte{0x00, 0xff, 0xfe})}},
			want: map[string][]byte{"bin": {0x00, 0xff, 0xfe}},
		},
		{
			name: "headers_b64 wins over headers for the same key",
			req: ProduceRequest{
				Headers:    map[string]string{"bin": "0xdeadbeef", "text": "keep"},
				HeadersB64: map[string]string{"bin": "3q2+7w=="},
			},
			want: map[string][]byte{"bin": {0xde, 0xad, 0xbe, 0xef}, "text": []byte("keep")},
		},
		{
			name: "literal 0x string in headers stays literal",
			req:  ProduceRequest{Headers: map[string]string{"h": "0xdeadbeef"}},
			want: map[string][]byte{"h": []byte("0xdeadbeef")},
		},
		{
			name: "headers_b64 url-safe alphabet tolerated",
			req:  ProduceRequest{HeadersB64: map[string]string{"bin": base64.URLEncoding.EncodeToString([]byte{0xfb, 0xff, 0xfe})}},
			want: map[string][]byte{"bin": {0xfb, 0xff, 0xfe}},
		},
		{
			name: "headers_b64 raw (unpadded) tolerated",
			req:  ProduceRequest{HeadersB64: map[string]string{"bin": base64.RawStdEncoding.EncodeToString([]byte{0xde, 0xad, 0xbe})}},
			want: map[string][]byte{"bin": {0xde, 0xad, 0xbe}},
		},
		{
			name:    "invalid base64 in headers_b64",
			req:     ProduceRequest{HeadersB64: map[string]string{"bin": "not!base64"}},
			wantErr: `header "bin": invalid base64`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, err := buildRecord("t", tc.req)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Nil(t, rec)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)

			// Iteration order over a Go map is random, so compare as a set while
			// asserting each key appears exactly once.
			require.Len(t, rec.Headers, len(tc.want), "unexpected number of headers: %+v", rec.Headers)
			seen := make(map[string]int, len(rec.Headers))
			for _, h := range rec.Headers {
				seen[h.Key]++
				want, ok := tc.want[h.Key]
				require.True(t, ok, "unexpected header key %q", h.Key)
				assert.Equal(t, want, h.Value, "header %q value mismatch", h.Key)
			}
			for k := range tc.want {
				assert.Equal(t, 1, seen[k], "header %q must be emitted exactly once", k)
			}
		})
	}
}

// TestRoundTrip_BinaryHeader is the regression guard for the copy path: a
// non-UTF-8 header value observed by recordToMessage must be reproducible
// byte-for-byte by buildRecord. Before HeadersB64 existed, the copy re-produced
// the "0x…" hex display string as ASCII text.
func TestRoundTrip_BinaryHeader(t *testing.T) {
	t.Parallel()

	rawBinary := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0xff}
	require.False(t, utf8.Valid(rawBinary), "test fixture must not be valid UTF-8")

	src := &kgo.Record{
		Topic:     "src",
		Partition: 3,
		Offset:    42,
		Timestamp: time.UnixMilli(1_700_000_000_000),
		Key:       []byte("k"),
		Value:     []byte("v"),
		Headers: []kgo.RecordHeader{
			{Key: "bin", Value: rawBinary},
			{Key: "text", Value: []byte("plain")},
			{Key: "looks-hex", Value: []byte("0xdeadbeef")},
		},
	}

	msg := recordToMessage(src)

	// The display rendering is preserved for the UI ...
	assert.Equal(t, "0x"+hex.EncodeToString(rawBinary), msg.Headers["bin"])
	assert.Equal(t, "plain", msg.Headers["text"])
	assert.Equal(t, "0xdeadbeef", msg.Headers["looks-hex"])
	// ... and the raw bytes travel in HeadersB64, for binary headers only.
	require.NotNil(t, msg.HeadersB64)
	assert.Len(t, msg.HeadersB64, 1)
	assert.Equal(t, base64.StdEncoding.EncodeToString(rawBinary), msg.HeadersB64["bin"])

	rec, err := buildRecord("dst", ProduceRequest{
		Key:        msg.Key,
		Value:      msg.Value,
		Headers:    msg.Headers,
		HeadersB64: msg.HeadersB64,
	})
	require.NoError(t, err)

	got := make(map[string][]byte, len(rec.Headers))
	for _, h := range rec.Headers {
		_, dup := got[h.Key]
		require.False(t, dup, "header %q emitted more than once", h.Key)
		got[h.Key] = h.Value
	}
	assert.Equal(t, rawBinary, got["bin"], "binary header must round-trip byte-for-byte")
	assert.Equal(t, []byte("plain"), got["text"])
	assert.Equal(t, []byte("0xdeadbeef"), got["looks-hex"], "a literal 0x… string must stay literal")
}

// TestRoundTrip_NoBinaryHeadersLeavesMapNil keeps HeadersB64 out of JSON
// responses when there is nothing binary to carry.
func TestRoundTrip_NoBinaryHeadersLeavesMapNil(t *testing.T) {
	t.Parallel()

	msg := recordToMessage(&kgo.Record{
		Headers: []kgo.RecordHeader{{Key: "a", Value: []byte("1")}},
	})
	assert.Nil(t, msg.HeadersB64, "no binary header must leave HeadersB64 nil (omitempty)")

	noHeaders := recordToMessage(&kgo.Record{})
	assert.Nil(t, noHeaders.Headers)
	assert.Nil(t, noHeaders.HeadersB64)
}

// TestRoundTrip_ZeroLengthValue is the regression guard for zero-length
// payloads: a record with a non-nil empty key/value must not come back as a
// tombstone / nil key after a copy.
func TestRoundTrip_ZeroLengthValue(t *testing.T) {
	t.Parallel()

	src := &kgo.Record{Key: []byte{}, Value: []byte{}}
	msg := recordToMessage(src)

	// decodeBytes reports zero-length payloads as encoding "empty".
	require.Equal(t, "empty", msg.KeyEncoding)
	require.Equal(t, "empty", msg.ValueEncoding)

	rec, err := buildRecord("dst", ProduceRequest{
		Key:           msg.Key,
		KeyEncoding:   msg.KeyEncoding,
		Value:         msg.Value,
		ValueEncoding: msg.ValueEncoding,
	})
	require.NoError(t, err)

	require.NotNil(t, rec.Key, "zero-length key must not become nil (that changes partitioning)")
	assert.Len(t, rec.Key, 0)
	require.NotNil(t, rec.Value, "zero-length value must not become a tombstone")
	assert.Len(t, rec.Value, 0)

	// Contrast: the same payload sent as "text" is a deliberate tombstone.
	tombstone, err := buildRecord("dst", ProduceRequest{Value: "", ValueEncoding: "text"})
	require.NoError(t, err)
	assert.Nil(t, tombstone.Value)
}

// TestRoundTrip_NullPayloadStaysNull covers the tombstone direction of the
// round trip: a nil source value must stay nil.
func TestRoundTrip_NullPayloadStaysNull(t *testing.T) {
	t.Parallel()

	msg := recordToMessage(&kgo.Record{Key: nil, Value: nil})
	require.Equal(t, "null", msg.KeyEncoding)
	require.Equal(t, "null", msg.ValueEncoding)

	// "null" is not a produce encoding; callers map it to "text" with an empty
	// value, which decodeProducePayload turns back into nil.
	rec, err := buildRecord("dst", ProduceRequest{Key: "", KeyEncoding: "text", Value: "", ValueEncoding: "text"})
	require.NoError(t, err)
	assert.Nil(t, rec.Key)
	assert.Nil(t, rec.Value)
}

func TestDecodeBase64Tolerant(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     string
		want   []byte
		wantOK bool
	}{
		{name: "std padded", in: "aGk=", want: []byte("hi"), wantOK: true},
		{name: "raw std unpadded", in: "aGk", want: []byte("hi"), wantOK: true},
		{name: "url safe", in: "-_8=", want: []byte{0xfb, 0xff}, wantOK: true},
		{name: "empty string decodes to zero length", in: "", want: []byte{}, wantOK: true},
		{name: "garbage", in: "!!!!", wantOK: false},
		{name: "garbage with spaces", in: "not base64", wantOK: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := decodeBase64Tolerant(tc.in)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
