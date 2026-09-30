// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// srUsersEnv starts a topic on a cluster with the fake Schema Registry and
// produces values, one record each, in order.
func srUsersEnv(t *testing.T, values ...[]byte) *kfakeEnv {
	t.Helper()
	srURL := startFakeSchemaRegistry(t)
	env := newKfakeEnv(t, "users", 1, func(c *config.ClusterConfig) {
		c.SchemaRegistry = config.SchemaRegistryConfig{URL: srURL}
	})
	recs := make([]*kgo.Record, 0, len(values))
	for _, v := range values {
		recs = append(recs, &kgo.Record{Value: v})
	}
	env.produce(t, recs...)
	return env
}

func TestFetchRawMessageValue_DecodesSchemaRegistryValues(t *testing.T) {
	t.Parallel()
	avroFrame := avroUserFrame(t, 1, "alice")
	jsonFrame := srFrame(jsonUserSchemaID, []byte(`{"id":2}`))
	unknownFrame := srFrame(4711, []byte(`{"id":3}`))
	protoFrame := srFrame(protoUserSchemaID, []byte{0x00, 0x08, 0x04})
	env := srUsersEnv(t, avroFrame, jsonFrame, unknownFrame, protoFrame, []byte(`{"plain":true}`))
	ctx := context.Background()

	fetch := func(offset int64, opts RawValueOptions) *RawMessageValue {
		t.Helper()
		raw, err := env.reg.FetchRawMessageValue(ctx, kfakeCluster, env.topic, 0, offset, opts)
		require.NoError(t, err)
		return raw
	}

	t.Run("avro is served as the decoded JSON of the list", func(t *testing.T) {
		raw := fetch(0, RawValueOptions{})
		assert.JSONEq(t, `{"id":1,"name":"alice"}`, string(raw.Value))
		assert.Equal(t, "application/json", raw.ContentType)
		assert.Equal(t, "json", raw.Extension)
		assert.Equal(t, "avro", raw.DecodedFormat)
	})

	t.Run("json schema is served without the wire-format framing", func(t *testing.T) {
		raw := fetch(1, RawValueOptions{})
		assert.Equal(t, `{"id":2}`, string(raw.Value))
		assert.Equal(t, "application/json", raw.ContentType)
		assert.Equal(t, "json", raw.Extension)
		assert.Equal(t, "json_schema", raw.DecodedFormat)
	})

	t.Run("wire bytes on request", func(t *testing.T) {
		assert.Equal(t, &RawMessageValue{Value: avroFrame, ContentType: "application/octet-stream", Extension: "bin"},
			fetch(0, RawValueOptions{WireBytes: true}))
		assert.Equal(t, &RawMessageValue{Value: jsonFrame, ContentType: "application/octet-stream", Extension: "bin"},
			fetch(1, RawValueOptions{WireBytes: true}), "framed bytes are binary even with a JSON body")
	})

	t.Run("an undecodable framed value falls back to the wire bytes", func(t *testing.T) {
		assert.Equal(t, &RawMessageValue{Value: unknownFrame, ContentType: "application/octet-stream", Extension: "bin"},
			fetch(2, RawValueOptions{}), "unknown schema id")
		assert.Equal(t, &RawMessageValue{Value: protoFrame, ContentType: "application/octet-stream", Extension: "bin"},
			fetch(3, RawValueOptions{}), "protobuf is not decoded")
	})

	t.Run("an unframed value is unchanged", func(t *testing.T) {
		want := &RawMessageValue{Value: []byte(`{"plain":true}`), ContentType: "application/json", Extension: "json"}
		assert.Equal(t, want, fetch(4, RawValueOptions{}))
		assert.Equal(t, want, fetch(4, RawValueOptions{WireBytes: true}))
	})
}

// Without a Schema Registry the list never decodes, so neither does /raw.
func TestFetchRawMessageValue_WithoutSchemaRegistryServesWireBytes(t *testing.T) {
	t.Parallel()
	env := newKfakeEnv(t, "users", 1, nil)
	frame := avroUserFrame(t, 1, "alice")
	env.produce(t, &kgo.Record{Value: frame})

	raw, err := env.reg.FetchRawMessageValue(context.Background(), kfakeCluster, env.topic, 0, 0, RawValueOptions{})
	require.NoError(t, err)
	assert.Equal(t, &RawMessageValue{Value: frame, ContentType: "application/octet-stream", Extension: "bin"}, raw)
}

// The masking check covers the wire bytes too: they carry the same data.
func TestFetchRawMessageValue_MaskedSchemaRegistryValueIsRefusedInEveryForm(t *testing.T) {
	t.Parallel()
	srURL := startFakeSchemaRegistry(t)
	env := newKfakeEnv(t, "users", 1, func(c *config.ClusterConfig) {
		c.SchemaRegistry = config.SchemaRegistryConfig{URL: srURL}
		c.DataMasking = []config.MaskingRule{{Fields: []string{"$.name"}}}
	})
	env.produce(t, &kgo.Record{Value: avroUserFrame(t, 1, "alice")})

	for _, opts := range []RawValueOptions{{}, {WireBytes: true}} {
		_, err := env.reg.FetchRawMessageValue(context.Background(), kfakeCluster, env.topic, 0, 0, opts)
		require.ErrorIs(t, err, ErrValueMasked, "%+v", opts)
	}
}

func TestRawValueFor(t *testing.T) {
	t.Parallel()
	frame := srFrame(avroUserSchemaID, []byte{0x02, 0x02, 'a'})
	decoded := decodedValue{rendered: `{"id":1,"name":"a"}`, format: "avro", ok: true}

	t.Run("the decoded value is checked against the limit", func(t *testing.T) {
		_, err := rawValueFor(frame, decoded, false, int64(len(decoded.rendered))-1)
		require.ErrorIs(t, err, ErrValueTooLarge, "decoding grew the value past the limit")

		raw, err := rawValueFor(frame, decoded, false, int64(len(decoded.rendered)))
		require.NoError(t, err)
		assert.Equal(t, &RawMessageValue{Value: []byte(decoded.rendered), ContentType: "application/json", Extension: "json", DecodedFormat: "avro"}, raw)
	})

	t.Run("the wire bytes skip the decoded limit", func(t *testing.T) {
		raw, err := rawValueFor(frame, decoded, true, int64(len(frame)))
		require.NoError(t, err)
		assert.Equal(t, frame, raw.Value)
	})

	t.Run("a decoded primitive is still JSON", func(t *testing.T) {
		raw, err := rawValueFor(frame, decodedValue{rendered: `"abc"`, format: "avro", ok: true}, false, 100)
		require.NoError(t, err)
		assert.Equal(t, "application/json", raw.ContentType)
	})

	t.Run("a failed decode serves the wire bytes as binary", func(t *testing.T) {
		raw, err := rawValueFor(frame, decodedValue{format: "unknown"}, false, 100)
		require.NoError(t, err)
		assert.Equal(t, &RawMessageValue{Value: frame, ContentType: "application/octet-stream", Extension: "bin"}, raw)
	})
}
