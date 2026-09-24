// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"strings"
	"testing"

	"github.com/antchfx/xpath"
	"github.com/ohler55/ojg/jp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// bigJSONWithNeedle builds a JSON object whose serialized size exceeds
// maxMessageValueBytes, with a padding field that pushes a needle field past
// the 64 KB truncation boundary.
func bigJSONWithNeedle(needle string) []byte {
	pad := strings.Repeat("x", maxMessageValueBytes+4096)
	return []byte(`{"padding":"` + pad + `","needle":"` + needle + `"}`)
}

// TestRecordToMessage_TruncatesLargeValue is a regression guard: the
// existing (list/consume) path must keep truncating large values exactly as
// before, so response sizes for the message list stay bounded.
func TestRecordToMessage_TruncatesLargeValue(t *testing.T) {
	t.Parallel()

	val := bigJSONWithNeedle("secret-marker")
	rec := &kgo.Record{Value: val}

	msg := recordToMessage(rec)

	assert.True(t, msg.ValueTruncated, "large value must be truncated for the consume/list path")
	assert.Len(t, msg.Value, maxMessageValueBytes)
	assert.Equal(t, int64(len(val)), msg.ValueSizeBytes, "original size must still be reported")
	assert.NotContains(t, msg.Value, "secret-marker", "needle placed past the truncation boundary must not survive truncation")
}

// TestRecordToMatchMessage_KeepsFullValue proves the search-path helper never
// truncates, so a needle placed past maxMessageValueBytes survives and is
// available for matching — the actual bug being fixed here.
func TestRecordToMatchMessage_KeepsFullValue(t *testing.T) {
	t.Parallel()

	val := bigJSONWithNeedle("secret-marker")
	rec := &kgo.Record{Value: val}

	msg := recordToMatchMessage(rec)

	assert.False(t, msg.ValueTruncated, "the full-decode path must not truncate")
	assert.Equal(t, string(val), msg.Value)
	assert.Contains(t, msg.Value, "secret-marker", "needle past the old truncation boundary must be present")
}

// TestContainsMatcher_FindsNeedlePastTruncationBoundary is a fast,
// broker-free regression test for the "contains" search mode: matching
// against the full (untruncated) message must find a needle that a
// 64 KB-capped match would have missed.
func TestContainsMatcher_FindsNeedlePastTruncationBoundary(t *testing.T) {
	t.Parallel()

	val := bigJSONWithNeedle("secret-marker")
	rec := &kgo.Record{Value: val}

	cm := &containsMatcher{needle: "secret-marker", zones: []SearchZone{ZoneValue}}

	fullMsg := recordToMatchMessage(rec)
	hit, err := cm.match(&fullMsg)
	require.NoError(t, err)
	assert.True(t, hit, "contains match against the full value must find the needle")

	truncatedMsg := recordToMessage(rec)
	missedHit, err := cm.match(&truncatedMsg)
	require.NoError(t, err)
	assert.False(t, missedHit, "sanity check: the same needle is unreachable once truncated")
}

// TestJSONPathMatcher_ParsesLargeValue is a fast, broker-free regression test
// for JSONPath mode: a value larger than the truncation cap must still parse
// and match when evaluated against the full (untruncated) message — today's
// bug truncates first, which corrupts the JSON and turns every large record
// into a parse error that silently drops it from the results.
func TestJSONPathMatcher_ParsesLargeValue(t *testing.T) {
	t.Parallel()

	val := bigJSONWithNeedle("secret-marker")
	rec := &kgo.Record{Value: val}

	expr, err := jp.ParseString("$.needle")
	require.NoError(t, err)
	pm, err := newPathMatcher(jsonPathEval(expr), OpEq, "secret-marker")
	require.NoError(t, err)

	fullMsg := recordToMatchMessage(rec)
	hit, err := pm.match(&fullMsg)
	require.NoError(t, err, "JSONPath must parse the full, untruncated JSON without error")
	assert.True(t, hit, "JSONPath match against the full value must find the needle")

	// Sanity check: matching the truncated value either errors (invalid JSON,
	// cut mid-structure) or simply misses — either way it must not silently
	// report the same hit, proving the truncated path is indeed broken today.
	truncatedMsg := recordToMessage(rec)
	missedHit, _ := pm.match(&truncatedMsg)
	assert.False(t, missedHit, "sanity check: the same needle is unreachable once truncated")
}

// TestXPathMatcher_ParsesLargeValue mirrors the JSONPath regression test
// above for XPath mode: a large XML value with a needle placed past the
// truncation boundary must still parse and match against the full record.
func TestXPathMatcher_ParsesLargeValue(t *testing.T) {
	t.Parallel()

	pad := strings.Repeat("x", maxMessageValueBytes+4096)
	val := []byte(`<root><padding>` + pad + `</padding><status>needle-shipped</status></root>`)
	rec := &kgo.Record{Value: val}

	expr, err := xpath.Compile("/root/status")
	require.NoError(t, err)
	pm, err := newPathMatcher(xmlPathEval(expr), OpEq, "needle-shipped")
	require.NoError(t, err)

	fullMsg := recordToMatchMessage(rec)
	hit, err := pm.match(&fullMsg)
	require.NoError(t, err, "XPath must parse the full, untruncated XML without error")
	assert.True(t, hit, "XPath match against the full value must find the needle")
}
