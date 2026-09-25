// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// ProduceRequest is the canonical input for producing a single record.
//
// Key/Value are strings; set their corresponding Encoding to "base64" to
// transport raw bytes (e.g. Avro payloads). Empty Key/Value with encoding
// "text" produces a nil key/value (tombstone-style); use encoding "empty" to
// produce a zero-length but non-nil key/value instead.
type ProduceRequest struct {
	Partition     *int32            `json:"partition,omitempty"`
	Key           string            `json:"key"`
	Value         string            `json:"value"`
	KeyEncoding   string            `json:"key_encoding,omitempty"`
	ValueEncoding string            `json:"value_encoding,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`

	// HeadersB64 supplies header values as standard-base64 raw bytes, for headers
	// whose value is not valid UTF-8 and therefore cannot round-trip through
	// Headers. A key present in both wins here.
	HeadersB64 map[string]string `json:"headers_b64,omitempty"`
}

// ProduceResult is the outcome of a successful produce.
type ProduceResult struct {
	Topic       string `json:"topic"`
	Partition   int32  `json:"partition"`
	Offset      int64  `json:"offset"`
	TimestampMs int64  `json:"timestamp_ms"`
}

// Produce writes one record to the given topic and waits for broker acknowledgement.
func (r *Registry) Produce(ctx context.Context, cluster, topic string, req ProduceRequest) (*ProduceResult, error) {
	cl, err := r.Client(cluster)
	if err != nil {
		return nil, err
	}

	rec, err := buildRecord(topic, req)
	if err != nil {
		return nil, err
	}

	res := cl.ProduceSync(ctx, rec)
	if err := res.FirstErr(); err != nil {
		return nil, fmt.Errorf("produce: %w", err)
	}
	if len(res) == 0 || res[0].Record == nil {
		return nil, errors.New("produce: empty broker response")
	}
	out := res[0].Record
	return &ProduceResult{
		Topic:       out.Topic,
		Partition:   out.Partition,
		Offset:      out.Offset,
		TimestampMs: out.Timestamp.UnixMilli(),
	}, nil
}

// ProduceBatch produces every request in reqs to topic in a single
// ProduceSync call, returning the number of records the broker acknowledged
// and the first error encountered. A partial result is normal on error:
// callers (e.g. the topic-copy job) report `produced` as progress before
// surfacing err.
func (r *Registry) ProduceBatch(ctx context.Context, cluster, topic string, reqs []ProduceRequest) (produced int, err error) {
	if len(reqs) == 0 {
		return 0, nil
	}

	cl, err := r.Client(cluster)
	if err != nil {
		return 0, err
	}

	// Decode everything up front: a single bad request fails the whole batch
	// without producing anything, so the caller can report a clean 0.
	recs := make([]*kgo.Record, 0, len(reqs))
	for i, req := range reqs {
		rec, err := buildRecord(topic, req)
		if err != nil {
			return 0, fmt.Errorf("record %d: %w", i, err)
		}
		recs = append(recs, rec)
	}

	// ProduceSync appends to its result slice from the per-record promise, so
	// ProduceResults is in completion order, not input order. Only the count of
	// nil-error entries is meaningful; a "successful prefix" cannot be derived.
	res := cl.ProduceSync(ctx, recs...)
	for _, pr := range res {
		if pr.Err == nil {
			produced++
		}
	}
	if err := res.FirstErr(); err != nil {
		return produced, fmt.Errorf("produce: %w", err)
	}
	return produced, nil
}

// buildRecord turns a ProduceRequest into a kgo.Record for topic. It is the
// single place where produce encoding rules live, shared by Produce and
// ProduceBatch so the two paths can never diverge.
func buildRecord(topic string, req ProduceRequest) (*kgo.Record, error) {
	keyBytes, err := decodeProducePayload(req.Key, req.KeyEncoding)
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}
	valBytes, err := decodeProducePayload(req.Value, req.ValueEncoding)
	if err != nil {
		return nil, fmt.Errorf("value: %w", err)
	}

	rec := &kgo.Record{
		Topic: topic,
		Key:   keyBytes,
		Value: valBytes,
		// unsetPartition, not the zero value: partition 0 is a real partition,
		// so the client-side partitioner needs a distinguishable "you choose".
		Partition: unsetPartition,
	}
	if req.Partition != nil && *req.Partition >= 0 {
		rec.Partition = *req.Partition
	}

	if n := len(req.Headers) + len(req.HeadersB64); n > 0 {
		rec.Headers = make([]kgo.RecordHeader, 0, n)
	}
	// A key present in both maps is emitted exactly once, from HeadersB64:
	// only the base64 form can carry non-UTF-8 bytes losslessly.
	for k, v := range req.Headers {
		if _, ok := req.HeadersB64[k]; ok {
			continue
		}
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	for k, v := range req.HeadersB64 {
		b, ok := decodeBase64Tolerant(v)
		if !ok {
			return nil, fmt.Errorf("header %q: invalid base64", k)
		}
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: b})
	}

	return rec, nil
}

// decodeProducePayload converts a (value, encoding) pair to bytes.
//
// Accepted encodings:
//
//   - "" or "text": value is taken as a UTF-8 string. An empty value produces
//     nil, i.e. a nil record key/value, so the produce API can write
//     tombstones.
//   - "base64": value is base64 (standard, URL-safe or raw-standard, tried in
//     that order). An empty value produces nil, exactly as for "text".
//   - "empty": the value string is ignored and a non-nil zero-length slice is
//     returned.
//
// "empty" exists because "text" cannot express a zero-length payload: it
// collapses to nil, which Kafka encodes as null on the wire. That distinction
// matters when reproducing records verbatim (topic copy). A zero-length value
// re-produced as nil becomes a tombstone and deletes the key on a compacted
// destination topic; a zero-length key re-produced as nil also changes
// partitioning, because franz-go hashes an empty key but round-robins a nil
// one.
func decodeProducePayload(value, encoding string) ([]byte, error) {
	switch encoding {
	case "", "text":
		if value == "" {
			return nil, nil
		}
		return []byte(value), nil
	case "empty":
		return []byte{}, nil
	case "base64":
		if value == "" {
			return nil, nil
		}
		b, ok := decodeBase64Tolerant(value)
		if !ok {
			return nil, fmt.Errorf("invalid base64")
		}
		return b, nil
	default:
		return nil, fmt.Errorf("unsupported encoding %q", encoding)
	}
}

// decodeBase64Tolerant decodes standard, URL-safe and raw-standard base64, in
// that order. It reports whether any of them succeeded.
func decodeBase64Tolerant(value string) ([]byte, bool) {
	if b, err := base64.StdEncoding.DecodeString(value); err == nil {
		return b, true
	}
	if b, err := base64.URLEncoding.DecodeString(value); err == nil {
		return b, true
	}
	if b, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return b, true
	}
	return nil, false
}
