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
// "text" produces a nil key/value (tombstone-style).
type ProduceRequest struct {
	Partition     *int32            `json:"partition,omitempty"`
	Key           string            `json:"key"`
	Value         string            `json:"value"`
	KeyEncoding   string            `json:"key_encoding,omitempty"`
	ValueEncoding string            `json:"value_encoding,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
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
	}
	if req.Partition != nil {
		rec.Partition = *req.Partition
	}
	for k, v := range req.Headers {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
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

// decodeProducePayload converts a (value, encoding) pair to bytes.
// Accepted encodings: "" or "text" (UTF-8 string), "base64" (standard or URL-safe).
// An empty text value produces nil (record key/value nil) to allow tombstones.
func decodeProducePayload(value, encoding string) ([]byte, error) {
	switch encoding {
	case "", "text":
		if value == "" {
			return nil, nil
		}
		return []byte(value), nil
	case "base64":
		if value == "" {
			return nil, nil
		}
		if b, err := base64.StdEncoding.DecodeString(value); err == nil {
			return b, nil
		}
		if b, err := base64.URLEncoding.DecodeString(value); err == nil {
			return b, nil
		}
		if b, err := base64.RawStdEncoding.DecodeString(value); err == nil {
			return b, nil
		}
		return nil, fmt.Errorf("invalid base64")
	default:
		return nil, fmt.Errorf("unsupported encoding %q", encoding)
	}
}
