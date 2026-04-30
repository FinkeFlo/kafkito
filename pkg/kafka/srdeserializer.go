// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/hamba/avro/v2"
)

// SRDecodedMeta describes a successfully Schema-Registry-decoded payload.
// Empty Format means the payload was not SR-framed.
type SRDecodedMeta struct {
	Format   string `json:"format,omitempty"` // "avro" | "protobuf" | "json_schema"
	SchemaID int    `json:"schema_id,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Version  int    `json:"version,omitempty"`
}

// srEntry is the cached form of a schema looked up by id.
type srEntry struct {
	id         int
	subject    string
	version    int
	schemaType string
	parsedAvro avro.Schema // nil for non-AVRO entries
}

// SRDecoder resolves Schema-Registry-framed Kafka payloads.
//
// Layout per Confluent wire-format (KIP-NONE, but a de-facto standard):
//
//	byte 0   : magic byte 0x00
//	bytes 1-4: schema id (big-endian uint32)
//	bytes 5+ : encoded payload (Avro binary, Protobuf, JSON-schema, …)
//
// SRDecoder caches schemas by id forever (small N expected). Concurrent-safe.
type SRDecoder struct {
	sr *SchemaRegistryClient

	mu    sync.RWMutex
	cache map[uint32]srEntry
}

// NewSRDecoder builds a decoder backed by the given SR client. Returns nil
// when sr is nil so call-sites can do `if d != nil { … }` without extra checks.
func NewSRDecoder(sr *SchemaRegistryClient) *SRDecoder {
	if sr == nil {
		return nil
	}
	return &SRDecoder{sr: sr, cache: make(map[uint32]srEntry, 16)}
}

// IsSRFramed reports whether the payload starts with the Confluent wire-format
// magic byte and carries enough bytes for a schema id.
func IsSRFramed(b []byte) bool {
	return len(b) >= 5 && b[0] == 0x00
}

// Decode attempts to decode an SR-framed payload. Returns ok=false (without
// error) if the payload is not framed. Returns ok=false with an error if it is
// framed but decoding failed (caller should fall back to raw rendering and
// surface the error in metadata if desired).
func (d *SRDecoder) Decode(ctx context.Context, b []byte) (rendered string, meta SRDecodedMeta, ok bool, err error) {
	if d == nil || !IsSRFramed(b) {
		return "", SRDecodedMeta{}, false, nil
	}
	id := binary.BigEndian.Uint32(b[1:5])
	payload := b[5:]

	entry, err := d.lookup(ctx, id)
	if err != nil {
		return "", SRDecodedMeta{Format: "unknown", SchemaID: int(id)}, false, err
	}

	meta = SRDecodedMeta{
		Format:   formatFromSchemaType(entry.schemaType),
		SchemaID: entry.id,
		Subject:  entry.subject,
		Version:  entry.version,
	}

	switch meta.Format {
	case "avro":
		if entry.parsedAvro == nil {
			return "", meta, false, errors.New("avro schema not parsed")
		}
		var native any
		if err := avro.Unmarshal(entry.parsedAvro, payload, &native); err != nil {
			return "", meta, false, fmt.Errorf("avro unmarshal (id=%d): %w", id, err)
		}
		jb, err := json.Marshal(native)
		if err != nil {
			return "", meta, false, fmt.Errorf("avro->json marshal: %w", err)
		}
		return string(jb), meta, true, nil
	case "json_schema":
		// Body is plain JSON behind the framing — surface as-is.
		if !json.Valid(payload) {
			return "", meta, false, errors.New("framed JSON_SCHEMA payload is not valid JSON")
		}
		return string(payload), meta, true, nil
	case "protobuf":
		// Decoding Protobuf payloads requires fetching the .proto descriptor and
		// resolving message-index varints; deferred. Surface metadata + raw size.
		return fmt.Sprintf("(protobuf payload, %d bytes — descriptor decoding not yet implemented)", len(payload)), meta, false, nil
	default:
		return "", meta, false, fmt.Errorf("unsupported schema type %q", entry.schemaType)
	}
}

func (d *SRDecoder) lookup(ctx context.Context, id uint32) (srEntry, error) {
	d.mu.RLock()
	if e, ok := d.cache[id]; ok {
		d.mu.RUnlock()
		return e, nil
	}
	d.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()
	// re-check under write lock
	if e, ok := d.cache[id]; ok {
		return e, nil
	}

	sv, err := d.sr.GetSchemaByID(ctx, int(id))
	if err != nil {
		return srEntry{}, fmt.Errorf("fetch schema id %d: %w", id, err)
	}
	entry := srEntry{
		id:         sv.ID,
		subject:    sv.Subject,
		version:    sv.Version,
		schemaType: sv.SchemaType,
	}
	if formatFromSchemaType(sv.SchemaType) == "avro" {
		parsed, perr := avro.Parse(sv.Schema)
		if perr != nil {
			return srEntry{}, fmt.Errorf("parse avro schema id %d: %w", id, perr)
		}
		entry.parsedAvro = parsed
	}
	d.cache[id] = entry
	return entry, nil
}

func formatFromSchemaType(t string) string {
	switch t {
	case "", "AVRO":
		return "avro"
	case "PROTOBUF":
		return "protobuf"
	case "JSON":
		return "json_schema"
	default:
		return ""
	}
}
