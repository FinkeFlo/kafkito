// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/masking"
)

// recordDecoder turns records of one topic into Messages: raw rendering,
// Schema Registry decoding and masking.
type recordDecoder struct {
	topic string
	sr    *SRDecoder // nil when the cluster has no Schema Registry
	mask  *masking.Policy
}

func (r *Registry) recordDecoder(cluster, topic string) recordDecoder {
	return recordDecoder{topic: topic, sr: r.srDecoderFor(cluster), mask: r.MaskingPolicy(cluster)}
}

// message renders rec for a response: the value is truncated to
// maxMessageValueBytes, decoded via the Schema Registry and masked.
func (d recordDecoder) message(ctx context.Context, rec *kgo.Record) Message {
	m := recordToMessage(rec)
	m.applySRDecoder(ctx, d.sr, rec.Key, rec.Value, true)
	if !d.mask.IsEmpty() && m.Value != "" {
		if mv, did := d.mask.Apply(d.topic, m.Value); did {
			m.Value = mv
			m.Masked = true
		}
	}
	return m
}

// matchMessage renders rec for the search matchers: full, decoded, unmasked.
// It must never end up in a response; see recordToMatchMessage.
func (d recordDecoder) matchMessage(ctx context.Context, rec *kgo.Record) Message {
	m := recordToMatchMessage(rec)
	m.applySRDecoder(ctx, d.sr, rec.Key, rec.Value, false)
	return m
}

// recordToMessage renders a record for the consume/list path: the value is
// capped at maxMessageValueBytes so response sizes stay bounded. The search
// scan uses recordToMatchMessage instead, which must not truncate.
func recordToMessage(rec *kgo.Record) Message {
	m := Message{
		Partition:      rec.Partition,
		Offset:         rec.Offset,
		Timestamp:      rec.Timestamp.UnixMilli(),
		ValueSizeBytes: int64(len(rec.Value)),
	}
	m.Key, m.KeyEncoding, m.KeyB64 = decodeBytes(rec.Key, false)

	// Truncate the raw value bytes before decoding to prevent large payloads
	// from causing outsized string allocations. The full byte length is already
	// stored in ValueSizeBytes so the UI can show the original size.
	valBytes := rec.Value
	if int64(len(valBytes)) > maxMessageValueBytes {
		valBytes = valBytes[:maxMessageValueBytes]
		m.ValueTruncated = true
	}
	m.Value, m.ValueEncoding, m.ValueB64 = decodeBytes(valBytes, m.ValueTruncated)
	m.Headers, m.HeadersB64 = renderHeaders(rec.Headers, true)
	return m
}

// renderHeaders renders UTF-8 header values as-is and others as "0x…" hex.
// With withB64 it also returns the raw bytes of the hex-rendered values so
// a verbatim re-produce (topic copy) stays lossless.
func renderHeaders(hs []kgo.RecordHeader, withB64 bool) (rendered, b64 map[string]string) {
	if len(hs) == 0 {
		return nil, nil
	}
	rendered = make(map[string]string, len(hs))
	for _, h := range hs {
		if utf8.Valid(h.Value) {
			rendered[h.Key] = string(h.Value)
			continue
		}
		rendered[h.Key] = "0x" + hex.EncodeToString(h.Value)
		if withB64 {
			if b64 == nil {
				b64 = make(map[string]string, 1)
			}
			b64[h.Key] = base64.StdEncoding.EncodeToString(h.Value)
		}
	}
	return rendered, b64
}

// recordToMatchMessage builds the untruncated Message the search scan matches
// against. It exists because matching must see the full record content —
// truncating first (as the consume/list path does) would silently hide
// matches located past maxMessageValueBytes and would corrupt structured
// (JSONPath/XPath/JS) parsing of any record larger than that cap.
//
// It deliberately populates only the fields the matchers actually read
// (Partition, Offset, Timestamp, Key, Value, Headers). In particular it skips
// the base64 rendering of the raw value: on the list path that string is
// capped at maxMessageValueBytes, but over the full value it costs ~1.33x the
// record size per scanned record and no matcher ever reads it. It likewise
// skips json.Valid/validXML, since ValueEncoding is not read during matching
// either.
//
// Callers must not put the resulting Message into a response as-is; rebuild
// the hit via recordDecoder.message first so response sizes stay bounded
// exactly like every other consume path.
func recordToMatchMessage(rec *kgo.Record) Message {
	m := Message{
		Partition:      rec.Partition,
		Offset:         rec.Offset,
		Timestamp:      rec.Timestamp.UnixMilli(),
		ValueSizeBytes: int64(len(rec.Value)),
	}
	m.Key = renderForMatch(rec.Key)
	m.Value = renderForMatch(rec.Value)
	m.Headers, _ = renderHeaders(rec.Headers, false)
	return m
}

// renderForMatch returns the same rendered string decodeBytes produces for an
// untruncated input, without computing the encoding label or the base64 of the
// raw bytes. Keep this byte-identical to decodeBytes's rendered return value —
// TestRenderForMatchMatchesDecodeBytes guards that.
func renderForMatch(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	preview := b
	if len(preview) > binaryPreviewBytes {
		preview = preview[:binaryPreviewBytes]
	}
	return "0x" + hex.EncodeToString(preview)
}

// applySRDecoder runs the optional Schema-Registry decoder over key+value of m.
// When decoding succeeds, the rendered string + encoding are overwritten with
// the JSON form and the corresponding *SR meta is attached. When decoding
// fails, the raw render is kept and meta is still attached so the UI can show
// "schema id N (decode error)". With truncate the decoded value is capped at
// maxMessageValueBytes; the search scan matches on the untruncated form.
func (m *Message) applySRDecoder(ctx context.Context, dec *SRDecoder, rawKey, rawValue []byte, truncate bool) {
	if dec == nil {
		return
	}
	if rendered, meta, ok, _ := dec.Decode(ctx, rawKey); meta.Format != "" {
		if ok {
			m.Key = rendered
			m.KeyEncoding = meta.Format
			m.KeyB64 = ""
		}
		mm := meta
		m.KeySR = &mm
	}
	if rendered, meta, ok, _ := dec.Decode(ctx, rawValue); meta.Format != "" {
		if ok {
			m.Value = rendered
			if truncate && int64(len(m.Value)) > maxMessageValueBytes {
				m.Value = m.Value[:maxMessageValueBytes]
				m.ValueTruncated = true
			}
			m.ValueEncoding = meta.Format
			m.ValueB64 = ""
		}
		mm := meta
		m.ValueSR = &mm
	}
}

// decodeBytes detects json/text/binary and returns a rendered string plus encoding.
// For binary payloads, it returns a hex preview and full base64 in b64.
func decodeBytes(b []byte, truncated bool) (rendered, encoding, b64 string) {
	if b == nil {
		return "", "null", ""
	}
	if len(b) == 0 {
		return "", "empty", ""
	}
	if utf8.Valid(b) {
		trimmed := bytesTrimSpace(b)
		looksJSON := len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
		// A truncated value is cut off mid-structure, so json.Valid on it will
		// (almost) always fail even for genuinely JSON payloads — checking only
		// the first non-whitespace byte (which truncation never removes) is the
		// best signal available. The full value is validated for real on
		// demand when the UI loads it in full; a false positive here just
		// falls back to a plain-text render at that point.
		if looksJSON && (truncated || json.Valid(trimmed)) {
			return string(b), "json", ""
		}
		looksXML := len(trimmed) > 0 && trimmed[0] == '<'
		if looksXML && (truncated || validXML(trimmed)) {
			return string(b), "xml", ""
		}
		return string(b), "text", ""
	}
	preview := b
	if len(preview) > binaryPreviewBytes {
		preview = preview[:binaryPreviewBytes]
	}
	return "0x" + hex.EncodeToString(preview), "binary", base64.StdEncoding.EncodeToString(b)
}

// validXML reports whether b is a well-formed XML document by tokenizing it
// end to end — cheaper than building a DOM (xmlquery.Parse, used by the
// XPath search matcher) when all that's needed is a validity check.
func validXML(b []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		_, err := dec.Token()
		if err != nil {
			return errors.Is(err, io.EOF)
		}
	}
}

func bytesTrimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
