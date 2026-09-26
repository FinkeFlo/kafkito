// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// customersTopic is the topic the masking rules of these tests target.
const customersTopic = "customers"

// customerRules masks the email field of JSON values and every "iban-…"
// token in any value of customersTopic.
func customerRules(c *config.ClusterConfig) {
	c.DataMasking = []config.MaskingRule{{
		Topics: []string{"^" + customersTopic + "$"},
		Fields: []string{"$.email"},
		Regex:  []config.RegexMask{{Match: `iban-[a-z0-9]+`, Replacement: "iban-***"}},
	}}
}

func TestFetchRawMessageValue_RefusesMaskedRecords(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, customersTopic, 1, customerRules)
	env.produce(t,
		&kgo.Record{Value: []byte(`{"id":1,"email":"alice@example.com"}`)},
		&kgo.Record{Value: []byte(`{"id":2}`)},
		&kgo.Record{Value: []byte("transfer to iban-de001")},
		&kgo.Record{Value: []byte{0xff, 0x00, 0xfe}},
	)
	ctx := context.Background()

	for _, offset := range []int64{0, 2} {
		raw, err := env.reg.FetchRawMessageValue(ctx, kfakeCluster, env.topic, 0, offset)
		require.ErrorIs(t, err, ErrValueMasked, "offset %d", offset)
		assert.Nil(t, raw)
	}

	for offset, want := range map[int64][]byte{1: []byte(`{"id":2}`), 3: {0xff, 0x00, 0xfe}} {
		raw, err := env.reg.FetchRawMessageValue(ctx, kfakeCluster, env.topic, 0, offset)
		require.NoError(t, err, "the policy leaves offset %d unchanged", offset)
		assert.Equal(t, want, raw.Value)
	}
}

func TestFetchRawMessageValue_UnmaskedTopicIsUnchanged(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "plain", 1, customerRules)
	value := []byte(`{"id":1,"email":"alice@example.com"} iban-de001`)
	env.produce(t, &kgo.Record{Value: value})

	raw, err := env.reg.FetchRawMessageValue(context.Background(), kfakeCluster, env.topic, 0, 0)
	require.NoError(t, err, "no rule targets the topic")
	assert.Equal(t, value, raw.Value)
}

func TestFetchRawMessageValue_ChecksSchemaRegistryDecodedValue(t *testing.T) {
	t.Parallel()
	srURL := startFakeSchemaRegistry(t)
	env := newKfakeEnv(t, "users", 1, func(c *config.ClusterConfig) {
		c.SchemaRegistry = config.SchemaRegistryConfig{URL: srURL}
		c.DataMasking = []config.MaskingRule{{Fields: []string{"$.name"}}}
	})
	env.produce(t, &kgo.Record{Value: avroUserFrame(t, 1, "alice")})

	_, err := env.reg.FetchRawMessageValue(context.Background(), kfakeCluster, env.topic, 0, 0)
	require.ErrorIs(t, err, ErrValueMasked, "the decoded JSON has the masked field")
}

// A JSON value larger than the 64 KB preview is masked on its full decoded
// rendering: the cut-off preview does not parse, so masking it would leave
// JSONPath fields in clear text.
func TestConsume_MasksLargeValuesInFull(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, customersTopic, 1, customerRules)
	padding := strings.Repeat("y", maxMessageValueBytes)
	env.produce(t,
		&kgo.Record{Value: []byte(`{"email":"early@example.com","pad":"` + padding + `"}`)},
		&kgo.Record{Value: []byte(`{"pad":"` + padding + `","email":"late@example.com"}`)},
	)

	res := consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 10, From: FromStart})
	require.Len(t, res.Messages, 2)
	for _, m := range res.Messages {
		assert.True(t, m.Masked, "offset %d", m.Offset)
		assert.True(t, m.ValueTruncated, "offset %d", m.Offset)
		assert.Len(t, m.Value, maxMessageValueBytes)
		assert.NotContains(t, m.Value, "@example.com", "offset %d", m.Offset)
	}

	_, err := env.reg.FetchRawMessageValue(context.Background(), kfakeCluster, env.topic, 0, 1)
	require.ErrorIs(t, err, ErrValueMasked, "the field past 64 KB is masked, so the raw value is refused")
}

func TestConsume_MaskedBinaryValueDropsRawBase64(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, customersTopic, 1, func(c *config.ClusterConfig) {
		c.DataMasking = []config.MaskingRule{{Regex: []config.RegexMask{{Match: `^0x[0-9a-f]+$`}}}}
	})
	env.produce(t, &kgo.Record{Value: []byte{0xff, 0x00, 0xfe}})

	res := consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 10, From: FromStart})
	require.Len(t, res.Messages, 1)
	m := res.Messages[0]
	assert.True(t, m.Masked)
	assert.Equal(t, "***", m.Value)
	assert.Empty(t, m.ValueB64, "the base64 would carry the unmasked bytes")
}

func TestSearch_MatchesOnlyTheMaskedRendering(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, customersTopic, 1, customerRules)
	env.produce(t,
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS), Value: []byte(`{"id":1,"email":"alice@example.com","city":"Berlin"}`)},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 1), Value: []byte("payout iban-de001 to bob")},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 2), Value: []byte("<pay><to>iban-de002</to></pay>")},
		&kgo.Record{Timestamp: time.UnixMilli(fixtureBaseTS + 3), Value: []byte(`{"id":2,"email":"` + strings.Repeat("y", maxMessageValueBytes) + `carol@example.com"}`)},
	)

	noHits := []SearchOptions{
		{Value: "alice"},
		{Value: "de001"},
		{Value: "carol@example.com"},
		{Mode: SearchModeJSONPath, Path: "$.email", Op: OpEq, Value: "alice@example.com"},
		{Mode: SearchModeJSONPath, Path: "$.email", Op: OpContains, Value: "@"},
		{Mode: SearchModeJSONPath, Path: "$.email", Op: OpRegex, Value: "^a"},
		{Mode: SearchModeJSONPath, Path: "$[?(@.email == 'alice@example.com')]", Op: OpExists},
		{Mode: SearchModeXPath, Path: "//to", Op: OpEq, Value: "iban-de002"},
		{Mode: SearchModeJS, Value: `parsed && parsed.email && parsed.email.startsWith("a")`},
		{Mode: SearchModeJS, Value: `value.includes("de00")`},
	}
	for _, opts := range noHits {
		opts.Partition = -1
		res := searchTopic(t, env, opts)
		assert.Empty(t, res.Messages, "%+v finds masked content", opts)
		assert.Equal(t, 4, res.Stats.Scanned, "%+v", opts)
		assert.Zero(t, res.Stats.ParseErrors, "%+v", opts)
	}

	hits := map[string]SearchOptions{
		"Berlin": {Value: "Berlin"},
		"bob":    {Value: "to bob"},
		"xpath":  {Mode: SearchModeXPath, Path: "//to", Op: OpEq, Value: "iban-***"},
		"json":   {Mode: SearchModeJSONPath, Path: "$.email", Op: OpEq, Value: "***"},
	}
	for name, opts := range hits {
		opts.Partition = -1
		opts.Direction = DirOldestFirst
		res := searchTopic(t, env, opts)
		require.NotEmpty(t, res.Messages, name)
		for _, m := range res.Messages {
			assert.True(t, m.Masked, "%s: offset %d", name, m.Offset)
			assert.NotContains(t, m.Value, "alice", name)
			assert.NotContains(t, m.Value, "de00", name)
		}
	}
}

func TestSearch_UnmaskedTopicMatchesClearValue(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "plain", 1, customerRules)
	env.produce(t, &kgo.Record{Value: []byte(`{"email":"alice@example.com"}`)})

	res := searchTopic(t, env, SearchOptions{Partition: -1, Value: "alice"})
	require.Len(t, res.Messages, 1)
	assert.False(t, res.Messages[0].Masked)
}

// Parser and JS errors can quote the value they failed on. On a masked
// topic neither the response nor the log carries them. Not parallel: it
// swaps the default logger.
func TestSearch_ParseErrorsDoNotEchoValuesOnMaskedTopics(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	masked := newKfakeEnv(t, customersTopic, 1, customerRules)
	plain := newKfakeEnv(t, "plain", 1, customerRules)
	values := [][]byte{
		[]byte(`{"email": secret-json-token`),
		[]byte(`<pay><secret-xml-tag>x</other></pay>`),
	}
	for _, env := range []*kfakeEnv{masked, plain} {
		for _, v := range values {
			env.produce(t, &kgo.Record{Value: v})
		}
	}
	searches := []SearchOptions{
		{Partition: -1, Mode: SearchModeJSONPath, Path: "$.email", Op: OpExists},
		{Partition: -1, Mode: SearchModeXPath, Path: "//to", Op: OpExists},
		{Partition: -1, Mode: SearchModeJS, Value: `(() => { throw new Error("got " + value) })()`},
	}

	for _, opts := range searches {
		res := searchTopic(t, masked, opts)
		require.NotEmpty(t, res.Stats.ParseErrorOffsets, "%+v", opts)
		for _, pe := range res.Stats.ParseErrorOffsets {
			assert.Equal(t, parseErrorWithheld, pe.Error, "%+v", opts)
		}
	}
	assert.NotContains(t, logs.String(), "secret-")
	assert.Contains(t, logs.String(), "details withheld")

	// Without a masking rule the details stay available for debugging.
	res := searchTopic(t, plain, searches[2])
	require.NotEmpty(t, res.Stats.ParseErrorOffsets)
	assert.Contains(t, res.Stats.ParseErrorOffsets[0].Error, "secret-")
}
