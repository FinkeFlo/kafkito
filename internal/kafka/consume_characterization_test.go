// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// The tests in this file pin the observable behaviour of ConsumeMessages
// against an in-memory cluster: page contents and order, cursors, has_more,
// partial, truncation, encodings, Schema Registry decoding and masking.

const ordersTopic = "orders"

// consumePage runs one ConsumeMessages call with a short, test-sized timeout.
func consumePage(t *testing.T, env *kfakeEnv, opts ConsumeOptions) *ConsumeResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	res, err := env.reg.ConsumeMessages(ctx, kfakeCluster, env.topic, opts)
	require.NoError(t, err)
	return res
}

// nextPageOptions applies a returned cursor the way the HTTP layer does.
func nextPageOptions(base ConsumeOptions, c *Cursor) ConsumeOptions {
	next := base
	next.Offset = 0
	next.PartitionOffsets = nil
	next.CursorUpperBounds = nil
	if c.Direction == CursorBackward {
		next.From = FromEnd
		next.CursorUpperBounds = c.Partitions
	} else {
		next.From = FromOffset
		next.PartitionOffsets = c.Partitions
	}
	return next
}

// consumeAllPages follows NextCursor until it is nil and returns every page.
func consumeAllPages(t *testing.T, env *kfakeEnv, opts ConsumeOptions) []*ConsumeResult {
	t.Helper()
	var pages []*ConsumeResult
	cur := opts
	for range 100 {
		res := consumePage(t, env, cur)
		pages = append(pages, res)
		require.Equal(t, res.NextCursor != nil, res.HasMore, "has_more must mirror next_cursor")
		assert.False(t, res.Partial, "page %d must not be partial", len(pages))
		if res.NextCursor == nil {
			return pages
		}
		cur = nextPageOptions(opts, res.NextCursor)
	}
	t.Fatal("paging did not terminate")
	return nil
}

// pageSeqs flattens pages to fixture sequence numbers, keeping page borders.
func pageSeqs(t *testing.T, pages []*ConsumeResult) [][]int {
	t.Helper()
	out := make([][]int, 0, len(pages))
	for _, p := range pages {
		out = append(out, seqs(t, p.Messages))
	}
	return out
}

// seqsOfPartition lists the fixture sequence numbers stored on partition p.
func seqsOfPartition(fx []fixtureRecord, p int32, newestFirst bool) []int {
	var out []int
	for _, r := range fx {
		if r.Partition == p {
			out = append(out, r.Seq)
		}
	}
	if newestFirst {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// chunk splits s into consecutive slices of size n.
func chunk(s []int, n int) [][]int {
	var out [][]int
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func newOrdersEnv(t *testing.T) (*kfakeEnv, []fixtureRecord) {
	t.Helper()
	env := newKfakeEnv(t, ordersTopic, 3, nil)
	return env, env.produceOrdersFixture(t, 24)
}

func TestConsumeCharacterization_FromStartAllPartitionsPagesForward(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	pages := consumeAllPages(t, env, ConsumeOptions{Partition: -1, Limit: 5, From: FromStart})

	assert.Equal(t, chunk(seqRange(0, 23), 5), pageSeqs(t, pages))
	first := pages[0].NextCursor
	require.NotNil(t, first)
	assert.Equal(t, CursorForward, first.Direction)
	// seqs 0..4 are p0 offsets 0-2, p1 offset 0 and p2 offset 0.
	assert.Equal(t, map[int32]int64{0: 3, 1: 1, 2: 1}, first.Partitions)
}

func TestConsumeCharacterization_FromEndAllPartitionsPagesBackward(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	pages := consumeAllPages(t, env, ConsumeOptions{Partition: -1, Limit: 5, From: FromEnd})

	assert.Equal(t, chunk(seqRange(23, 0), 5), pageSeqs(t, pages))
	first := pages[0].NextCursor
	require.NotNil(t, first)
	assert.Equal(t, CursorBackward, first.Direction)
	// seqs 23..19: p1 offsets 7 and 6, p0 offsets 11 and 10, p2 offset 3.
	assert.Equal(t, map[int32]int64{0: 10, 1: 6, 2: 3}, first.Partitions)
}

func TestConsumeCharacterization_DefaultFromIsEnd(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	res := consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 3})

	assert.Equal(t, []int{23, 22, 21}, seqs(t, res.Messages))
	assert.True(t, res.HasMore)
}

func TestConsumeCharacterization_SinglePartitionPagesBothDirections(t *testing.T) {
	t.Parallel()
	env, fx := newOrdersEnv(t)

	for _, p := range []int32{0, 1, 2} {
		backward := consumeAllPages(t, env, ConsumeOptions{Partition: p, Limit: 3, From: FromEnd})
		assert.Equal(t, chunk(seqsOfPartition(fx, p, true), 3), pageSeqs(t, backward), "partition %d backward", p)

		forward := consumeAllPages(t, env, ConsumeOptions{Partition: p, Limit: 3, From: FromStart})
		assert.Equal(t, chunk(seqsOfPartition(fx, p, false), 3), pageSeqs(t, forward), "partition %d forward", p)
		for _, pg := range append(backward, forward...) {
			for _, m := range pg.Messages {
				assert.Equal(t, p, m.Partition)
			}
		}
	}
}

func TestConsumeCharacterization_FromOffsetSinglePartition(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	pages := consumeAllPages(t, env, ConsumeOptions{Partition: 0, Limit: 4, From: FromOffset, Offset: 5})

	// p0 holds the even seqs; offset 5 is seq 10.
	assert.Equal(t, [][]int{{10, 12, 14, 16}, {18, 20, 22}}, pageSeqs(t, pages))
}

func TestConsumeCharacterization_FromOffsetBelowStartClampsToStart(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	res := consumePage(t, env, ConsumeOptions{Partition: 2, Limit: 10, From: FromOffset, Offset: -5})

	assert.Equal(t, []int{3, 9, 15, 21}, seqs(t, res.Messages))
	assert.False(t, res.HasMore)
	assert.Nil(t, res.NextCursor)
}

func TestConsumeCharacterization_PartitionOffsets(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	res := consumePage(t, env, ConsumeOptions{
		Partition:        -1,
		Limit:            50,
		From:             FromOffset,
		PartitionOffsets: map[int32]int64{0: 10, 2: 3},
	})

	assert.Equal(t, []int{20, 21, 22}, seqs(t, res.Messages))
	assert.False(t, res.HasMore)

	paged := consumeAllPages(t, env, ConsumeOptions{
		Partition:        -1,
		Limit:            2,
		From:             FromOffset,
		PartitionOffsets: map[int32]int64{0: 8, 1: 5},
	})
	// p0 offsets 8..11 = seqs 16,18,20,22; p1 offsets 5..7 = seqs 17,19,23.
	assert.Equal(t, [][]int{{16, 17}, {18, 19}, {20, 22}, {23}}, pageSeqs(t, paged))
}

func TestConsumeCharacterization_FromOffsetAllPartitionsNeedsPartitionOffsets(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	_, err := env.reg.ConsumeMessages(context.Background(), kfakeCluster, env.topic, ConsumeOptions{
		Partition: -1, From: FromOffset, Offset: 3, Timeout: time.Second,
	})

	require.EqualError(t, err, "from=offset with partition=-1 requires partition_offsets")
}

func TestConsumeCharacterization_UnknownPartitionAndCluster(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	_, err := env.reg.ConsumeMessages(context.Background(), kfakeCluster, env.topic, ConsumeOptions{Partition: 9})
	require.EqualError(t, err, `partition 9 not found in topic "orders" on cluster "kf"`)

	_, err = env.reg.ConsumeMessages(context.Background(), "nope", env.topic, ConsumeOptions{Partition: -1})
	require.ErrorIs(t, err, ErrUnknownCluster)
}

func TestConsumeCharacterization_FromTimestamp(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	from := fixtureBaseTS + 5_000
	to := fixtureBaseTS + 15_000

	res := consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 4, From: FromTimestamp, FromTSMs: from, ToTSMs: to})
	assert.Equal(t, seqRange(5, 8), seqs(t, res.Messages))
	require.NotNil(t, res.NextCursor)
	assert.Equal(t, CursorForward, res.NextCursor.Direction)

	// A lower bound alone pages forward to the end of the topic.
	pages := consumeAllPages(t, env, ConsumeOptions{Partition: -1, Limit: 4, From: FromTimestamp, FromTSMs: from})
	assert.Equal(t, chunk(seqRange(5, 23), 4), pageSeqs(t, pages))

	// An empty From with a time bound defaults to FromTimestamp.
	res = consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 50, FromTSMs: from, ToTSMs: to})
	assert.Equal(t, seqRange(5, 14), seqs(t, res.Messages))
	assert.False(t, res.HasMore)

	// Open-ended lower bound only.
	res = consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 50, From: FromTimestamp, FromTSMs: fixtureBaseTS + 20_000})
	assert.Equal(t, seqRange(20, 23), seqs(t, res.Messages))
}

func TestConsumeCharacterization_FromEndAndStartWithinTimeWindow(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	from := fixtureBaseTS + 5_000
	to := fixtureBaseTS + 15_000

	backward := consumeAllPages(t, env, ConsumeOptions{Partition: -1, Limit: 3, From: FromEnd, FromTSMs: from, ToTSMs: to})
	assert.Equal(t, chunk(seqRange(14, 5), 3), pageSeqs(t, backward))

	forward := consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 3, From: FromStart, FromTSMs: from, ToTSMs: to})
	assert.Equal(t, seqRange(5, 7), seqs(t, forward.Messages))
	forward = consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 50, From: FromStart, FromTSMs: from, ToTSMs: to})
	assert.Equal(t, seqRange(5, 14), seqs(t, forward.Messages))
	assert.False(t, forward.HasMore)
}

func TestConsumeCharacterization_EmptyRangeReturnsEmptyPage(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)

	res := consumePage(t, env, ConsumeOptions{Partition: -1, From: FromTimestamp, FromTSMs: fixtureBaseTS + 999_000})

	assert.Equal(t, &ConsumeResult{Messages: []Message{}}, res)
}

func TestConsumeCharacterization_LimitDefaultsAndCap(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "many", 1, nil)
	recs := make([]*kgo.Record, 0, 520)
	for i := range 520 {
		recs = append(recs, &kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)), Value: []byte("v")})
	}
	env.produce(t, recs...)

	res := consumePage(t, env, ConsumeOptions{Partition: -1, From: FromStart})
	assert.Len(t, res.Messages, defaultConsumeLimit)

	res = consumePage(t, env, ConsumeOptions{Partition: -1, From: FromStart, Limit: 10_000})
	assert.Len(t, res.Messages, maxConsumeLimit)
	assert.True(t, res.HasMore)
	assert.Equal(t, map[int32]int64{0: maxConsumeLimit}, res.NextCursor.Partitions)
}

func TestConsumeCharacterization_EncodingsTruncationAndHeaders(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "shapes", 1, nil)
	bigJSON := []byte(`{"pad":"` + strings.Repeat("x", maxMessageValueBytes) + `"}`)
	bigBinary := make([]byte, maxMessageValueBytes+10)
	for i := range bigBinary {
		bigBinary[i] = byte(0x80 + i%64)
	}
	env.produce(t,
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS), Key: []byte("json"), Value: []byte(`{"a":1}`)},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 1), Key: []byte("xml"), Value: []byte(`<a>1</a>`)},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 2), Key: []byte("text"), Value: []byte(`{not json`)},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 3), Key: nil, Value: nil},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 4), Key: []byte{}, Value: []byte{}},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 5), Key: []byte{0xff, 0x00}, Value: []byte("v"), Headers: []kgo.RecordHeader{
			{Key: "text", Value: []byte("plain")},
			{Key: "bin", Value: []byte{0xff, 0xfe}},
		}},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 6), Key: []byte("bigjson"), Value: bigJSON},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 7), Key: []byte("bigbin"), Value: bigBinary},
	)

	res := consumePage(t, env, ConsumeOptions{Partition: 0, From: FromStart})
	require.Len(t, res.Messages, 8)
	m := res.Messages

	assert.Equal(t, Message{Offset: 0, Timestamp: fixtureBaseTS, Key: "json", KeyEncoding: "text", Value: `{"a":1}`, ValueEncoding: "json", ValueSizeBytes: 7}, m[0])
	assert.Equal(t, Message{Offset: 1, Timestamp: fixtureBaseTS + 1, Key: "xml", KeyEncoding: "text", Value: `<a>1</a>`, ValueEncoding: "xml", ValueSizeBytes: 8}, m[1])
	assert.Equal(t, Message{Offset: 2, Timestamp: fixtureBaseTS + 2, Key: "text", KeyEncoding: "text", Value: `{not json`, ValueEncoding: "text", ValueSizeBytes: 9}, m[2])
	assert.Equal(t, Message{Offset: 3, Timestamp: fixtureBaseTS + 3, KeyEncoding: "null", ValueEncoding: "null"}, m[3])
	assert.Equal(t, Message{Offset: 4, Timestamp: fixtureBaseTS + 4, KeyEncoding: "empty", ValueEncoding: "empty"}, m[4])
	assert.Equal(t, Message{
		Offset: 5, Timestamp: fixtureBaseTS + 5,
		Key: "0xff00", KeyEncoding: "binary", KeyB64: base64.StdEncoding.EncodeToString([]byte{0xff, 0x00}),
		Value: "v", ValueEncoding: "text", ValueSizeBytes: 1,
		Headers:    map[string]string{"text": "plain", "bin": "0xfffe"},
		HeadersB64: map[string]string{"bin": base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe})},
	}, m[5])

	assert.True(t, m[6].ValueTruncated)
	assert.Equal(t, int64(len(bigJSON)), m[6].ValueSizeBytes)
	assert.Equal(t, string(bigJSON[:maxMessageValueBytes]), m[6].Value)
	assert.Equal(t, "json", m[6].ValueEncoding, "a truncated JSON value keeps its json encoding")
	assert.Empty(t, m[6].ValueB64)

	assert.True(t, m[7].ValueTruncated)
	assert.Equal(t, int64(len(bigBinary)), m[7].ValueSizeBytes)
	assert.Equal(t, "binary", m[7].ValueEncoding)
	assert.Equal(t, "0x"+hex.EncodeToString(bigBinary[:binaryPreviewBytes]), m[7].Value)
	assert.Equal(t, base64.StdEncoding.EncodeToString(bigBinary[:maxMessageValueBytes]), m[7].ValueB64)
}

func TestConsumeCharacterization_SchemaRegistryDecoding(t *testing.T) {
	t.Parallel()
	srURL := startFakeSchemaRegistry(t)
	env := newKfakeEnv(t, "users", 1, func(c *config.ClusterConfig) {
		c.SchemaRegistry = config.SchemaRegistryConfig{URL: srURL}
	})
	bigName := strings.Repeat("n", maxMessageValueBytes)
	env.produce(t,
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS), Key: avroUserFrame(t, 1, "key"), Value: avroUserFrame(t, 2, "alice")},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 1), Key: []byte("plain"), Value: avroUserFrame(t, 3, bigName)},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 2), Key: []byte("raw"), Value: []byte(`{"not":"framed"}`)},
	)

	res := consumePage(t, env, ConsumeOptions{Partition: 0, From: FromStart})
	require.Len(t, res.Messages, 3)

	small := res.Messages[0]
	assert.JSONEq(t, `{"id":1,"name":"key"}`, small.Key)
	assert.Equal(t, "avro", small.KeyEncoding)
	assert.Empty(t, small.KeyB64)
	require.NotNil(t, small.KeySR)
	assert.Equal(t, avroUserSchemaID, small.KeySR.SchemaID)
	assert.JSONEq(t, `{"id":2,"name":"alice"}`, small.Value)
	assert.Equal(t, "avro", small.ValueEncoding)
	assert.Empty(t, small.ValueB64)
	require.NotNil(t, small.ValueSR)
	assert.Equal(t, SRDecodedMeta{Format: "avro", SchemaID: avroUserSchemaID, Subject: "users-value", Version: 1}, *small.ValueSR)
	assert.False(t, small.ValueTruncated)

	big := res.Messages[1]
	assert.Equal(t, "avro", big.ValueEncoding)
	assert.True(t, big.ValueTruncated, "decoded value longer than the cap is truncated")
	assert.Len(t, big.Value, maxMessageValueBytes)
	assert.Equal(t, "plain", big.Key)
	assert.Nil(t, big.KeySR)

	raw := res.Messages[2]
	assert.Equal(t, "json", raw.ValueEncoding)
	assert.Nil(t, raw.ValueSR)
}

func TestConsumeCharacterization_MaskingApplies(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, ordersTopic, 3, func(c *config.ClusterConfig) {
		c.DataMasking = []config.MaskingRule{{Fields: []string{"$.name"}, Replacement: "***"}}
	})
	env.produceOrdersFixture(t, 6)

	res := consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 2, From: FromStart})

	require.Len(t, res.Messages, 2)
	for i, m := range res.Messages {
		assert.True(t, m.Masked)
		assert.JSONEq(t, `{"seq":`+[]string{"0", "1"}[i]+`,"kind":"`+[]string{"even", "odd"}[i]+`","name":"***"}`, m.Value)
	}
}
