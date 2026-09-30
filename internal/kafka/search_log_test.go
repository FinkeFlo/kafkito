// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// searchLogContent is record content that must never reach the log.
const searchLogContent = "DE00-CUSTOMER-PII"

// A topic without a masking rule, like every topic of a private cluster,
// returns the parser and JS error texts to the searching user. The log only
// gets one line per search with the count per error kind and the location
// of the first skipped record. Not parallel: it swaps the default logger.
func TestSearch_LogsParseErrorKindsWithoutRecordContent(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	env := newKfakeEnv(t, "plain", 2, nil)
	env.produce(t,
		&kgo.Record{Partition: 1, Value: []byte(`{"ok":true}`)},
		&kgo.Record{Partition: 1, Value: []byte(`{"iban":"` + searchLogContent + `"`)},
		&kgo.Record{Partition: 1, Value: []byte(`<pay><` + searchLogContent + `>x</to></pay>`)},
		&kgo.Record{Partition: 1, Value: []byte(`loop ` + searchLogContent)},
	)

	cases := []struct {
		name        string
		opts        SearchOptions
		count       int
		kinds       map[string]int
		firstOffset int64
		// quotes is true when the error text quotes the record, as the
		// response to the searching user still does.
		quotes bool
	}{
		{
			name:        "jsonpath",
			opts:        SearchOptions{Mode: SearchModeJSONPath, Path: "$.iban", Op: OpExists},
			count:       1,
			kinds:       map[string]int{matchErrJSONParse: 1},
			firstOffset: 1,
		},
		{
			name:        "xpath",
			opts:        SearchOptions{Mode: SearchModeXPath, Path: "//to", Op: OpExists},
			count:       1,
			kinds:       map[string]int{matchErrXMLParse: 1},
			firstOffset: 2,
			quotes:      true,
		},
		{
			name: "js",
			opts: SearchOptions{Mode: SearchModeJS, Value: `
				if (value.startsWith("loop")) { while (true) {} }
				if (value.includes("DE00")) { throw new Error(value) }
				return false`},
			count:       3,
			kinds:       map[string]int{matchErrJS: 2, matchErrJSTimeout: 1},
			firstOffset: 1,
			quotes:      true,
		},
	}
	for _, tc := range cases {
		logs.Reset()
		tc.opts.Partition = -1
		tc.opts.Direction = DirOldestFirst
		res := searchTopic(t, env, tc.opts)

		require.Equal(t, tc.count, res.Stats.ParseErrors, tc.name)
		require.Len(t, res.Stats.ParseErrorOffsets, tc.count, tc.name)
		if tc.quotes {
			assert.Contains(t, res.Stats.ParseErrorOffsets[0].Error, searchLogContent, tc.name)
		}

		assert.NotContains(t, logs.String(), searchLogContent, tc.name)
		lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
		require.Len(t, lines, 1, "%s: one line per search: %s", tc.name, logs.String())
		var line struct {
			Level          string         `json:"level"`
			Msg            string         `json:"msg"`
			Count          int            `json:"count"`
			Kinds          map[string]int `json:"kinds"`
			FirstPartition int32          `json:"first_partition"`
			FirstOffset    int64          `json:"first_offset"`
		}
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &line), tc.name)
		assert.Equal(t, "WARN", line.Level, tc.name)
		assert.Equal(t, "search: skipped messages that could not be evaluated", line.Msg, tc.name)
		assert.Equal(t, tc.count, line.Count, tc.name)
		assert.Equal(t, tc.kinds, line.Kinds, tc.name)
		assert.Equal(t, int32(1), line.FirstPartition, tc.name)
		assert.Equal(t, tc.firstOffset, line.FirstOffset, tc.name)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &keys), tc.name)
		assert.NotContains(t, keys, "error", tc.name)
	}

	logs.Reset()
	res := searchTopic(t, env, SearchOptions{Partition: -1, Mode: SearchModeJS, Value: `value.includes("ok")`})
	require.Len(t, res.Messages, 1)
	assert.Empty(t, logs.String(), "a search without parse errors logs nothing")
}

func TestMatchErrorKind(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("scan: %w", &matchError{kind: matchErrXMLParse, err: errors.New("bad " + searchLogContent)})
	assert.Equal(t, matchErrXMLParse, matchErrorKind(wrapped))
	assert.Equal(t, "scan: bad "+searchLogContent, wrapped.Error(), "the text for the response is unchanged")
	assert.Equal(t, matchErrOther, matchErrorKind(errors.New(searchLogContent)), "an error without a kind")
}
