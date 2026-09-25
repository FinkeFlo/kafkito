// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// TestRenderForMatchMatchesDecodeBytes pins renderForMatch to decodeBytes:
// the search scan must match against byte-identical content to what the
// list path would render for the same (untruncated) input. If decodeBytes
// ever changes what it renders, this fails instead of silently making
// search and display disagree.
func TestRenderForMatchMatchesDecodeBytes(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"nil":            nil,
		"empty":          {},
		"plain text":     []byte("hello world"),
		"json object":    []byte(`{"a":1,"b":[2,3]}`),
		"json array":     []byte(`  [1,2,3]  `),
		"xml":            []byte(`<root><a>1</a></root>`),
		"broken json":    []byte(`{"a":1,`),
		"utf8 multibyte": []byte("grüße – 日本語 🎉"),
		"utf8 bom":       append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"a":1}`)...),
		"binary short":   {0x00, 0x01, 0x02, 0xFF},
		"binary long":    bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 64),
		"avro-ish":       append([]byte{0x00, 0x00, 0x00, 0x00, 0x01}, bytes.Repeat([]byte{0xFE}, 200)...),
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rendered, _, _ := decodeBytes(in, false)
			assert.Equal(t, rendered, renderForMatch(in))
		})
	}
}

// TestRecordToMatchMessage_BinaryKeepsPreviewWithoutB64 is the regression
// guard for the allocation fix: a binary record must still match against the
// same hex preview the list path shows, but must not pay for a base64
// rendering of the full value — no matcher reads ValueB64.
func TestRecordToMatchMessage_BinaryKeepsPreviewWithoutB64(t *testing.T) {
	t.Parallel()

	val := bytes.Repeat([]byte{0x00, 0xFF, 0x10}, 400_000) // ~1.2 MB, not valid UTF-8
	rec := &kgo.Record{Value: val, Key: []byte{0x01, 0x02}}

	match := recordToMatchMessage(rec)
	listed := recordToMessage(rec)

	require.NotEmpty(t, match.Value)
	assert.True(t, strings.HasPrefix(match.Value, "0x"), "binary values render as a hex preview")
	assert.Equal(t, listed.Value, match.Value, "search must see exactly what the list path renders")
	assert.Empty(t, match.ValueB64, "base64 of the full value is never read while matching")
	assert.Empty(t, match.KeyB64, "base64 of the key is never read while matching")
	assert.NotEmpty(t, listed.ValueB64, "the list path still carries base64 for the UI")
	assert.Equal(t, int64(len(val)), match.ValueSizeBytes)
}

// TestRecordToMatchMessage_KeepsFullUTF8Value proves a needle placed past
// maxMessageValueBytes survives into the string the matchers see.
func TestRecordToMatchMessage_KeepsFullUTF8Value(t *testing.T) {
	t.Parallel()

	val := bigJSONWithNeedle("needle-past-the-cap")
	rec := &kgo.Record{Value: val}

	match := recordToMatchMessage(rec)

	require.Greater(t, len(val), maxMessageValueBytes)
	assert.Equal(t, string(val), match.Value)
	assert.False(t, match.ValueTruncated)
}

// TestRecordToMatchMessage_CarriesMatcherFields checks the fields the
// matchers actually read are populated, including the hex rendering of
// non-UTF-8 header values.
func TestRecordToMatchMessage_CarriesMatcherFields(t *testing.T) {
	t.Parallel()

	rec := &kgo.Record{
		Partition: 3,
		Offset:    42,
		Key:       []byte("order-1"),
		Value:     []byte(`{"status":"shipped"}`),
		Headers: []kgo.RecordHeader{
			{Key: "trace", Value: []byte("abc")},
			{Key: "raw", Value: []byte{0xFF, 0x00}},
		},
	}

	match := recordToMatchMessage(rec)

	assert.Equal(t, int32(3), match.Partition)
	assert.Equal(t, int64(42), match.Offset)
	assert.Equal(t, "order-1", match.Key)
	assert.Equal(t, `{"status":"shipped"}`, match.Value) //nolint:testifylint // raw value passthrough must be byte-exact, not just JSON-equivalent
	assert.Equal(t, "abc", match.Headers["trace"])
	assert.Equal(t, "0xff00", match.Headers["raw"])
	assert.Equal(t, recordToMessage(rec).Headers["raw"], match.Headers["raw"])
}

// TestRecordToMatchMessage_JSONValueIsParseable guards the jsMatcher/JSONPath
// contract: the string handed to the matchers must still parse as JSON for a
// record whose value exceeds the list-path truncation cap.
func TestRecordToMatchMessage_JSONValueIsParseable(t *testing.T) {
	t.Parallel()

	val := bigJSONWithNeedle("needle-past-the-cap")
	match := recordToMatchMessage(&kgo.Record{Value: val})

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(match.Value), &parsed))
	assert.Equal(t, "needle-past-the-cap", parsed["needle"])
}

func benchRecord(size int, binary bool) *kgo.Record {
	if binary {
		v := make([]byte, size)
		for i := range v {
			v[i] = byte(i % 251)
			if v[i] < 0x80 {
				v[i] |= 0x80 // force invalid UTF-8
			}
		}
		return &kgo.Record{Value: v}
	}
	pad := strings.Repeat("x", size)
	return &kgo.Record{Value: []byte(`{"padding":"` + pad + `","needle":"n"}`)}
}

// BenchmarkSearchDecode_Binary and BenchmarkSearchDecode_JSON are the standing
// reference for the per-scanned-record cost of the search path. Watch B/op:
// recordToMatchMessage must stay far below what a full decodeBytes of the same
// record costs, because the base64 of the full value is pure waste here.
func BenchmarkSearchDecode_Binary(b *testing.B) {
	rec := benchRecord(4<<20, true)
	b.ReportAllocs()
	for b.Loop() {
		_ = recordToMatchMessage(rec)
	}
}

func BenchmarkSearchDecode_JSON(b *testing.B) {
	rec := benchRecord(4<<20, false)
	b.ReportAllocs()
	for b.Loop() {
		_ = recordToMatchMessage(rec)
	}
}
