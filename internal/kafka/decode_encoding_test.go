// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kgo"
)

// TestDecodeBytes_JSON_Truncated_ReportsJSON verifies that a value cut off
// mid-structure (as happens once it crosses maxMessageValueBytes) is still
// labeled "json" as long as it starts with '{' or '[' — full json.Valid
// would (almost) always fail on a truncated payload, so the truncated case
// relies on the syntactic sniff alone.
func TestDecodeBytes_JSON_Truncated_ReportsJSON(t *testing.T) {
	truncated := []byte(`{"order":{"id":"ORD-1","items":[{"sku":"A"`) // deliberately unterminated
	_, enc, _ := decodeBytes(truncated, true)
	assert.Equal(t, "json", enc)
}

// TestDecodeBytes_JSON_NotTruncated_InvalidJSON_ReportsText verifies the
// full (non-truncated) case keeps requiring real validity: malformed JSON
// that was never truncated should not be mislabeled.
func TestDecodeBytes_JSON_NotTruncated_InvalidJSON_ReportsText(t *testing.T) {
	malformed := []byte(`{"order":{"id":"ORD-1"`) // genuinely malformed, not a truncation artifact
	_, enc, _ := decodeBytes(malformed, false)
	assert.Equal(t, "text", enc)
}

// TestDecodeBytes_XML_Truncated_ReportsXML mirrors the JSON case for XML:
// a value cut off mid-element should still be labeled "xml" based on the
// leading '<'.
func TestDecodeBytes_XML_Truncated_ReportsXML(t *testing.T) {
	truncated := []byte(`<order><id>ORD-1</id><items><item sku="A"`) // unterminated
	_, enc, _ := decodeBytes(truncated, true)
	assert.Equal(t, "xml", enc)
}

// TestDecodeBytes_XML_NotTruncated_ValidXML_ReportsXML checks a small,
// well-formed XML document (never truncated) is detected via full
// validation.
func TestDecodeBytes_XML_NotTruncated_ValidXML_ReportsXML(t *testing.T) {
	doc := []byte(`<?xml version="1.0"?><order><id>ORD-1</id></order>`)
	_, enc, _ := decodeBytes(doc, false)
	assert.Equal(t, "xml", enc)
}

// TestDecodeBytes_XML_NotTruncated_InvalidXML_ReportsText verifies malformed
// XML that was never truncated is not mislabeled.
func TestDecodeBytes_XML_NotTruncated_InvalidXML_ReportsText(t *testing.T) {
	malformed := []byte(`<order><id>ORD-1</id>`) // missing closing </order>
	_, enc, _ := decodeBytes(malformed, false)
	assert.Equal(t, "text", enc)
}

// TestDecodeBytes_PlainTextStartingWithAngleBracket_ReportsText guards
// against over-eager XML detection: text that merely starts with '<' but
// isn't well-formed XML (and wasn't truncated) must still render as text.
func TestDecodeBytes_PlainTextStartingWithAngleBracket_ReportsText(t *testing.T) {
	notXML := []byte(`<3 you too`)
	_, enc, _ := decodeBytes(notXML, false)
	assert.Equal(t, "text", enc)
}

// TestRecordToMessage_LargeXML_ReportsXML is an end-to-end check that a
// genuinely large (truncated) XML record is labeled "xml" through the full
// buildMessage path, not just the decodeBytes unit above.
func TestRecordToMessage_LargeXML_ReportsXML(t *testing.T) {
	padding := strings.Repeat("y", 128*1024)
	value := []byte(`<order><id>ORD-1</id><notes>` + padding + `</notes></order>`)
	rec := &kgo.Record{Value: value}
	m := recordToMessage(rec)
	assert.True(t, m.ValueTruncated)
	assert.Equal(t, "xml", m.ValueEncoding)
}
