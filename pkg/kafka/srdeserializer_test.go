// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/hamba/avro/v2"
	"github.com/stretchr/testify/require"
)

// TestSRDecoder_AvroRoundtrip wires SRDecoder against an httptest-backed
// fake schema registry, encodes a record with hamba/avro using the Confluent
// wire format ([0x00][be uint32 id][payload]) and asserts the decoder
// produces JSON + meaningful metadata.
func TestSRDecoder_AvroRoundtrip(t *testing.T) {
	t.Parallel()

	const schemaJSON = `{
		"type":"record",
		"name":"User",
		"fields":[
			{"name":"id","type":"long"},
			{"name":"name","type":"string"}
		]
	}`
	schema, err := avro.Parse(schemaJSON)
	require.NoError(t, err)

	const schemaID = 42

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
	defer srv.Close()

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
	require.True(t, ok, "decode should succeed")
	require.Equal(t, "avro", meta.Format)
	require.Equal(t, schemaID, meta.SchemaID)
	require.Equal(t, "users-value", meta.Subject)
	require.Equal(t, 3, meta.Version)
	require.True(t, strings.Contains(rendered, `"name":"alice"`), "rendered=%s", rendered)
	require.True(t, strings.Contains(rendered, `"id":7`), "rendered=%s", rendered)
}

func TestSRDecoder_NotFramed(t *testing.T) {
	t.Parallel()

	sr := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: "http://example.invalid"})
	dec := NewSRDecoder(sr)

	_, meta, ok, err := dec.Decode(context.Background(), []byte("plain text"))
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, SRDecodedMeta{}, meta)
}

func TestSRDecoder_NilSafe(t *testing.T) {
	t.Parallel()

	require.Nil(t, NewSRDecoder(nil))
	require.False(t, IsSRFramed(nil))
	require.False(t, IsSRFramed([]byte{0x00, 0x00, 0x00}))
	require.True(t, IsSRFramed([]byte{0x00, 0x00, 0x00, 0x00, 0x01}))
}
