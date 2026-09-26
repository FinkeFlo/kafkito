// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// The tests in this file pin the observable behaviour of SearchMessages
// against an in-memory cluster: matches, their order, the stats block
// (scanned, matched, cursors, resolved range, parse errors) and budgets.

func searchTopic(t *testing.T, env *kfakeEnv, opts SearchOptions) *SearchResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Second
	}
	res, err := env.reg.SearchMessages(ctx, kfakeCluster, env.topic, opts)
	require.NoError(t, err)
	return res
}

// withoutDurations zeroes the wall-clock part of the stats so the rest can
// be compared exactly.
func withoutDurations(t *testing.T, s SearchStats) SearchStats {
	t.Helper()
	require.Contains(t, s.Durations, "total")
	s.Durations = nil
	return s
}

// allOrdersRange is the resolved range of the 24-record orders fixture.
var allOrdersRange = map[int32]PartitionRange{0: {Start: 0, End: 12}, 1: {Start: 0, End: 8}, 2: {Start: 0, End: 4}}

func TestSearchCharacterization_ContainsValueBothDirections(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	newest := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Value: "rec-1", Zones: []SearchZone{ZoneValue}})
	assert.Equal(t, append(seqRange(19, 10), 1), seqs(t, newest.Messages))
	assert.Equal(t, SearchStats{
		Scanned:       24,
		Matched:       11,
		Direction:     DirNewestFirst,
		NextCursors:   map[int32]int64{0: 0, 1: 0, 2: 0},
		ResolvedRange: allOrdersRange,
	}, withoutDurations(t, newest.Stats))

	oldest := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Value: "rec-1", Direction: DirOldestFirst})
	assert.Equal(t, append([]int{1}, seqRange(10, 19)...), seqs(t, oldest.Messages))
	assert.Equal(t, SearchStats{
		Scanned:       24,
		Matched:       11,
		Direction:     DirOldestFirst,
		NextCursors:   map[int32]int64{0: 12, 1: 8, 2: 4},
		ResolvedRange: allOrdersRange,
	}, withoutDurations(t, oldest.Stats))
}

func TestSearchCharacterization_ContainsZones(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	oldest := func(needle string, zones ...SearchZone) []int {
		res := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Direction: DirOldestFirst, Mode: SearchModeContains, Value: needle, Zones: zones})
		assert.Equal(t, 24, res.Stats.Scanned)
		assert.Equal(t, len(res.Messages), res.Stats.Matched)
		return seqs(t, res.Messages)
	}

	assert.Equal(t, []int{2, 20, 21, 22, 23}, oldest("k-2", ZoneKey))
	assert.Empty(t, oldest("k-2", ZoneValue))
	assert.Equal(t, []int{2, 20, 21, 22, 23}, oldest("rec-2"), "the value zone is the default")
	assert.Equal(t, []int{7, 17}, oldest("7", ZoneHeaders))
	assert.Len(t, oldest("seq", ZoneHeaders), 24, "header keys are matched too")
	assert.Equal(t, []int{2, 12, 20, 21, 22, 23}, oldest("2", ZoneKey, ZoneHeaders))
	assert.Len(t, oldest(""), 24, "an empty needle matches everything")
}

func TestSearchCharacterization_LimitTruncatesButCountsAllMatches(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	res := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 3, Value: "rec"})

	assert.Equal(t, []int{23, 22, 21}, seqs(t, res.Messages))
	assert.Equal(t, SearchStats{
		Scanned:       24,
		Matched:       24,
		Direction:     DirNewestFirst,
		NextCursors:   map[int32]int64{0: 0, 1: 0, 2: 0},
		ResolvedRange: allOrdersRange,
	}, withoutDurations(t, res.Stats))
}

func TestSearchCharacterization_JSONPath(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	run := func(path string, op SearchOp, value string) []int {
		res := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Direction: DirOldestFirst, Mode: SearchModeJSONPath, Path: path, Op: op, Value: value})
		assert.Equal(t, 24, res.Stats.Scanned)
		assert.Zero(t, res.Stats.ParseErrors)
		return seqs(t, res.Messages)
	}

	assert.Equal(t, []int{1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23}, run("$.kind", OpEq, "odd"))
	assert.Equal(t, []int{21, 22, 23}, run("$.seq", OpGt, "20"))
	assert.Equal(t, []int{0, 1, 2}, run("$.seq", OpLte, "2"))
	assert.Equal(t, []int{3, 13, 23}, run("$.name", OpRegex, `3$`))
	assert.Empty(t, run("$.missing", OpExists, ""))
	assert.Len(t, run("$.name", OpExists, ""), 24)
}

func TestSearchCharacterization_JSONPathParseErrors(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "mixed", 1, nil)
	for i, v := range []string{`{"a":1}`, `{broken`, `hello`, `{"a":5}`, `[1,2`} {
		env.produce(t, &kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)), Value: []byte(v)})
	}

	for _, dir := range []SearchDirection{DirOldestFirst, DirNewestFirst} {
		res := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Direction: dir, Mode: SearchModeJSONPath, Path: "$.a", Op: OpGte, Value: "1"})

		offsets := []int64{}
		for _, m := range res.Messages {
			offsets = append(offsets, m.Offset)
		}
		if dir == DirOldestFirst {
			assert.Equal(t, []int64{0, 3}, offsets)
		} else {
			assert.Equal(t, []int64{3, 0}, offsets)
		}
		assert.Equal(t, 5, res.Stats.Scanned)
		assert.Equal(t, 2, res.Stats.Matched)
		assert.Equal(t, 2, res.Stats.ParseErrors)
		require.Len(t, res.Stats.ParseErrorOffsets, 2)
		assert.Equal(t, int64(1), res.Stats.ParseErrorOffsets[0].Offset)
		assert.Equal(t, int64(4), res.Stats.ParseErrorOffsets[1].Offset)
		for _, pe := range res.Stats.ParseErrorOffsets {
			assert.Equal(t, int32(0), pe.Partition)
			assert.NotEmpty(t, pe.Error)
		}
	}
}

func TestSearchCharacterization_XPath(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "xml", 1, nil)
	values := []string{
		`<order><id>1</id><kind>odd</kind></order>`,
		`<order><id>2</id><kind>even</kind></order>`,
		`<order><id>3</kind>`,
		`not xml`,
		`<order><id>5</id><kind>odd</kind></order>`,
	}
	for i, v := range values {
		env.produce(t, &kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)), Value: []byte(v)})
	}
	run := func(path string, op SearchOp, value string) *SearchResult {
		return searchTopic(t, env, SearchOptions{Partition: 0, Limit: 500, Direction: DirOldestFirst, Mode: SearchModeXPath, Path: path, Op: op, Value: value})
	}
	offsets := func(res *SearchResult) []int64 {
		out := []int64{}
		for _, m := range res.Messages {
			out = append(out, m.Offset)
		}
		return out
	}

	odd := run("//kind", OpEq, "odd")
	assert.Equal(t, []int64{0, 4}, offsets(odd))
	assert.Equal(t, 5, odd.Stats.Scanned)
	assert.Equal(t, 1, odd.Stats.ParseErrors)
	require.Len(t, odd.Stats.ParseErrorOffsets, 1)
	assert.Equal(t, int64(2), odd.Stats.ParseErrorOffsets[0].Offset)

	assert.Equal(t, []int64{0, 1, 4}, offsets(run("//id", OpExists, "")))
	assert.Equal(t, []int64{1, 4}, offsets(run("/order/id", OpGte, "2")))
}

func TestSearchCharacterization_JavaScript(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	run := func(expr string) *SearchResult {
		return searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Direction: DirOldestFirst, Mode: SearchModeJS, Value: expr})
	}

	assert.Equal(t, []int{0, 5, 10, 15, 20}, seqs(t, run("parsed.seq % 5 === 0").Messages))
	assert.Equal(t, []int{3, 9, 15, 21}, seqs(t, run("partition === 2").Messages))
	assert.Equal(t, []int{3, 13, 23}, seqs(t, run(`return key.endsWith("3")`).Messages))
	assert.Equal(t, []int{7}, seqs(t, run(`headers.seq === "7" && timestampMs > 0 && offset >= 0`).Messages))

	broken := run("parsed.nope.deeper")
	assert.Empty(t, broken.Messages)
	assert.Equal(t, 24, broken.Stats.ParseErrors, "runtime errors count as parse errors")
	assert.Len(t, broken.Stats.ParseErrorOffsets, 24)
}

func TestSearchCharacterization_InvalidQueriesAndUnknownCluster(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	ctx := context.Background()

	for _, opts := range []SearchOptions{
		{Mode: "fuzzy", Value: "x"},
		{Mode: SearchModeJSONPath, Path: "$[", Value: "x"},
		{Mode: SearchModeXPath, Path: "//[", Value: "x"},
		{Mode: SearchModeJS, Value: "return ("},
		{Mode: SearchModeJSONPath, Path: "$.a", Op: OpGt, Value: "abc"},
	} {
		_, err := env.reg.SearchMessages(ctx, kfakeCluster, env.topic, opts)
		require.Error(t, err, "%+v", opts)
	}

	_, err := env.reg.SearchMessages(ctx, "nope", env.topic, SearchOptions{})
	require.ErrorIs(t, err, ErrUnknownCluster)
}

func TestSearchCharacterization_PartitionFilter(t *testing.T) {
	t.Parallel()
	env, fx := newOrdersEnv(t)

	res := searchTopic(t, env, SearchOptions{Partition: 1, Limit: 500, Direction: DirOldestFirst, Value: "rec"})
	assert.Equal(t, seqsOfPartition(fx, 1, false), seqs(t, res.Messages))
	assert.Equal(t, map[int32]PartitionRange{1: {Start: 0, End: 8}}, res.Stats.ResolvedRange)
	assert.Equal(t, map[int32]int64{1: 8}, res.Stats.NextCursors)

	// An unknown partition is not an error for search: it resolves to an
	// empty range.
	missing := searchTopic(t, env, SearchOptions{Partition: 9, Value: "rec"})
	assert.Equal(t, &SearchResult{
		Messages: []Message{},
		Stats:    SearchStats{Direction: DirNewestFirst, ResolvedRange: map[int32]PartitionRange{}},
	}, missing)
}

func TestSearchCharacterization_TimeRange(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	res := searchTopic(t, env, SearchOptions{
		Partition: -1, Limit: 500, Direction: DirOldestFirst, Value: "rec",
		FromTS: fixtureBaseTS + 5_000, ToTS: fixtureBaseTS + 15_000,
	})

	assert.Equal(t, seqRange(5, 14), seqs(t, res.Messages))
	assert.Equal(t, SearchStats{
		Scanned:       10,
		Matched:       10,
		Direction:     DirOldestFirst,
		NextCursors:   map[int32]int64{0: 8, 1: 5, 2: 2},
		ResolvedRange: map[int32]PartitionRange{0: {Start: 3, End: 8}, 1: {Start: 1, End: 5}, 2: {Start: 1, End: 2}},
	}, withoutDurations(t, res.Stats))
}

func TestSearchCharacterization_Cursors(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	cursors := map[int32]int64{0: 6, 1: 4, 2: 2}

	newest := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Value: "rec", Cursors: cursors})
	assert.Equal(t, seqRange(11, 0), seqs(t, newest.Messages))
	assert.Equal(t, map[int32]PartitionRange{0: {Start: 0, End: 6}, 1: {Start: 0, End: 4}, 2: {Start: 0, End: 2}}, newest.Stats.ResolvedRange)
	assert.Equal(t, map[int32]int64{0: 0, 1: 0, 2: 0}, newest.Stats.NextCursors)
	assert.False(t, newest.Stats.MoreAvailable)

	oldest := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Direction: DirOldestFirst, Value: "rec", Cursors: cursors})
	assert.Equal(t, seqRange(12, 23), seqs(t, oldest.Messages))
	assert.Equal(t, map[int32]int64{0: 12, 1: 8, 2: 4}, oldest.Stats.NextCursors)

	done := searchTopic(t, env, SearchOptions{Partition: -1, Direction: DirOldestFirst, Value: "rec", Cursors: map[int32]int64{0: 12, 1: 8, 2: 4}})
	assert.Equal(t, &SearchResult{
		Messages: []Message{},
		Stats:    SearchStats{Direction: DirOldestFirst, ResolvedRange: map[int32]PartitionRange{}},
	}, done)
}

// chainSearch follows next_cursors the way the UI's "Search more" does until
// more_available is false and returns every matched message.
func chainSearch(t *testing.T, env *kfakeEnv, opts SearchOptions) (msgs []Message, calls []SearchStats) {
	t.Helper()
	for range 200 {
		res := searchTopic(t, env, opts)
		msgs = append(msgs, res.Messages...)
		calls = append(calls, res.Stats)
		if !res.Stats.MoreAvailable {
			return msgs, calls
		}
		opts.Cursors = res.Stats.NextCursors
	}
	t.Fatal("search chain did not terminate")
	return nil, nil
}

func TestSearchCharacterization_BudgetChainCoversEveryRecordOnce(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	for _, dir := range []SearchDirection{DirOldestFirst, DirNewestFirst} {
		msgs, calls := chainSearch(t, env, SearchOptions{Partition: -1, Limit: 500, Budget: 5, Direction: dir, Value: "rec"})

		assert.ElementsMatch(t, seqRange(0, 23), seqs(t, msgs), "direction %s", dir)
		assert.True(t, calls[0].BudgetExhausted, "direction %s", dir)
		assert.GreaterOrEqual(t, calls[0].Scanned, 5)
		total := 0
		for _, c := range calls {
			total += c.Scanned
		}
		assert.Equal(t, 24, total, "every record is scanned exactly once across the chain (%s)", dir)
	}
}

func TestSearchCharacterization_NewestFirstChunksLargePartition(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "large", 1, nil)
	const n = 9000
	recs := make([]*kgo.Record, 0, n)
	for i := range n {
		v := fmt.Sprintf("v-%d", i)
		if i%1000 == 0 {
			v = fmt.Sprintf("hit-%d", i)
		}
		recs = append(recs, &kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)), Value: []byte(v)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, r := range recs {
		r.Topic = env.topic
	}
	require.NoError(t, env.cl.ProduceSync(ctx, recs...).FirstErr())

	offsets := func(msgs []Message) []int64 {
		out := []int64{}
		for _, m := range msgs {
			out = append(out, m.Offset)
		}
		return out
	}

	newest := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Value: "hit"})
	assert.Equal(t, []int64{8000, 7000, 6000, 5000, 4000, 3000, 2000, 1000, 0}, offsets(newest.Messages))
	assert.Equal(t, SearchStats{
		Scanned:       n,
		Matched:       9,
		Direction:     DirNewestFirst,
		NextCursors:   map[int32]int64{0: 0},
		ResolvedRange: map[int32]PartitionRange{0: {Start: 0, End: n}},
	}, withoutDurations(t, newest.Stats))

	oldest := searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Direction: DirOldestFirst, Value: "hit"})
	assert.Equal(t, []int64{0, 1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000}, offsets(oldest.Messages))
	assert.Equal(t, n, oldest.Stats.Scanned)
	assert.Equal(t, map[int32]int64{0: n}, oldest.Stats.NextCursors)

	msgs, calls := chainSearch(t, env, SearchOptions{Partition: -1, Limit: 500, Budget: 5000, Value: "hit"})
	assert.Equal(t, []int64{8000, 7000, 6000, 5000, 4000, 3000, 2000, 1000, 0}, offsets(msgs))
	assert.True(t, calls[0].BudgetExhausted)
	assert.True(t, calls[0].MoreAvailable)
}

func TestSearchCharacterization_LargeValuesMatchInFullAndReturnTruncated(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "big", 1, nil)
	big := `{"padding":"` + strings.Repeat("x", maxMessageValueBytes+4096) + `","needle":"deep-marker"}`
	env.produce(t,
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS), Value: []byte(`{"needle":"other"}`)},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 1), Value: []byte(big)},
	)

	for _, opts := range []SearchOptions{
		{Mode: SearchModeContains, Value: "deep-marker"},
		{Mode: SearchModeJSONPath, Path: "$.needle", Op: OpEq, Value: "deep-marker"},
		{Mode: SearchModeJS, Value: `parsed.needle === "deep-marker"`},
	} {
		opts.Partition = -1
		res := searchTopic(t, env, opts)
		require.Len(t, res.Messages, 1, "mode %s", opts.Mode)
		m := res.Messages[0]
		assert.Equal(t, int64(1), m.Offset)
		assert.True(t, m.ValueTruncated)
		assert.Len(t, m.Value, maxMessageValueBytes)
		assert.Equal(t, int64(len(big)), m.ValueSizeBytes)
		assert.Equal(t, "json", m.ValueEncoding)
		assert.Zero(t, res.Stats.ParseErrors)
	}
}

func TestSearchCharacterization_SchemaRegistryDecodedValues(t *testing.T) {
	t.Parallel()
	srURL := startFakeSchemaRegistry(t)
	env := newKfakeEnv(t, "users", 1, func(c *config.ClusterConfig) {
		c.SchemaRegistry = config.SchemaRegistryConfig{URL: srURL}
	})
	longName := strings.Repeat("n", maxMessageValueBytes) + "tail-marker"
	env.produce(t,
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS), Value: avroUserFrame(t, 1, "alice")},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 1), Value: avroUserFrame(t, 2, "bob")},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 2), Value: avroUserFrame(t, 3, longName)},
	)

	bob := searchTopic(t, env, SearchOptions{Partition: -1, Mode: SearchModeJSONPath, Path: "$.name", Op: OpEq, Value: "bob"})
	require.Len(t, bob.Messages, 1)
	assert.JSONEq(t, `{"id":2,"name":"bob"}`, bob.Messages[0].Value)
	assert.Equal(t, "avro", bob.Messages[0].ValueEncoding)
	require.NotNil(t, bob.Messages[0].ValueSR)
	assert.Equal(t, avroUserSchemaID, bob.Messages[0].ValueSR.SchemaID)

	tail := searchTopic(t, env, SearchOptions{Partition: -1, Value: "tail-marker"})
	require.Len(t, tail.Messages, 1, "the decoded value is matched in full")
	assert.Equal(t, int64(2), tail.Messages[0].Offset)
	assert.True(t, tail.Messages[0].ValueTruncated)
	assert.Len(t, tail.Messages[0].Value, maxMessageValueBytes)

	ids := searchTopic(t, env, SearchOptions{Partition: -1, Direction: DirOldestFirst, Mode: SearchModeJS, Value: "parsed.id >= 2"})
	assert.Len(t, ids.Messages, 2)
}

func TestSearchCharacterization_MatchesMaskedValue(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, ordersTopic, 3, func(c *config.ClusterConfig) {
		c.DataMasking = []config.MaskingRule{{Fields: []string{"$.name"}, Replacement: "***"}}
	})
	env.produceOrdersFixture(t, 12)

	hidden := searchTopic(t, env, SearchOptions{Partition: -1, Value: "rec-5"})
	assert.Empty(t, hidden.Messages, "masked content is not searchable")
	assert.Equal(t, 12, hidden.Stats.Scanned)

	// The matchers see the masked rendering, which is re-encoded JSON with
	// sorted keys, exactly as the response shows it.
	visible := searchTopic(t, env, SearchOptions{Partition: -1, Value: `"name":"***","seq":5}`})
	require.Len(t, visible.Messages, 1)
	assert.True(t, visible.Messages[0].Masked)
	assert.JSONEq(t, `{"seq":5,"kind":"odd","name":"***"}`, visible.Messages[0].Value)
}
