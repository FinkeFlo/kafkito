// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hamba/avro/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

// TestSRDecoder_AvroRoundtrip_DecodesPayloadAndExposesMetadata wires
// SRDecoder against an httptest-backed fake schema registry, encodes a
// record with hamba/avro using the Confluent wire format
// ([0x00][be uint32 id][payload]) and asserts both the rendered JSON and
// the metadata derived from the registry response.
func TestSRDecoder_AvroRoundtrip_DecodesPayloadAndExposesMetadata(t *testing.T) {
	t.Parallel()

	const schemaJSON = `{
		"type":"record",
		"name":"User",
		"fields":[
			{"name":"id","type":"long"},
			{"name":"name","type":"string"}
		]
	}`
	const schemaID = 42

	schema, err := avro.Parse(schemaJSON)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/schemas/ids/42", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema":     schemaJSON,
			"schemaType": "AVRO",
			"subject":    "users-value",
			"version":    3,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	sr := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL})
	dec := NewSRDecoder(sr)
	require.NotNil(t, dec)

	payload, err := avro.Marshal(schema, map[string]any{"id": int64(7), "name": "alice"})
	require.NoError(t, err)
	framed := make([]byte, 5+len(payload))
	framed[0] = 0x00
	binary.BigEndian.PutUint32(framed[1:5], schemaID)
	copy(framed[5:], payload)
	require.True(t, IsSRFramed(framed))

	rendered, meta, ok, err := dec.Decode(context.Background(), framed)

	require.NoError(t, err)
	require.True(t, ok, "decode must succeed for valid Avro framed payload")

	t.Run("renders_payload_as_json", func(t *testing.T) {
		assert.Contains(t, rendered, `"name":"alice"`, "rendered=%s", rendered)
		assert.Contains(t, rendered, `"id":7`, "rendered=%s", rendered)
	})

	t.Run("exposes_schema_metadata", func(t *testing.T) {
		assert.Equal(t, "avro", meta.Format)
		assert.Equal(t, schemaID, meta.SchemaID)
		assert.Equal(t, "users-value", meta.Subject)
		assert.Equal(t, 3, meta.Version)
	})
}

func TestSRDecoder_NotFramed_ReturnsFalseAndZeroMeta(t *testing.T) {
	t.Parallel()

	sr := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: "http://example.invalid"})
	dec := NewSRDecoder(sr)

	_, meta, ok, err := dec.Decode(context.Background(), []byte("plain text"))

	require.NoError(t, err)
	assert.False(t, ok, "plain text must not decode as framed")
	assert.Equal(t, SRDecodedMeta{}, meta)
}

func TestNewSRDecoder_ReturnsNil_OnNilClient(t *testing.T) {
	t.Parallel()

	dec := NewSRDecoder(nil)

	assert.Nil(t, dec, "NewSRDecoder(nil) must return nil so callers fall through")
}

func TestIsSRFramed_RecognisesByteLayout(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  []byte
		want bool
	}{
		{name: "rejects_nil", raw: nil, want: false},
		{name: "rejects_short_buffer", raw: []byte{0x00, 0x00, 0x00}, want: false},
		{name: "accepts_minimal_framed_header", raw: []byte{0x00, 0x00, 0x00, 0x00, 0x01}, want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsSRFramed(tc.raw))
		})
	}
}
