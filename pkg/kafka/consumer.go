// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Message is a single decoded Kafka record for the viewer.
type Message struct {
	Partition     int32             `json:"partition"`
	Offset        int64             `json:"offset"`
	Timestamp     int64             `json:"timestamp_ms"`
	Key           string            `json:"key,omitempty"`
	KeyEncoding   string            `json:"key_encoding"`
	KeyB64        string            `json:"key_b64,omitempty"`
	Value         string            `json:"value,omitempty"`
	ValueEncoding string            `json:"value_encoding"`
	ValueB64      string            `json:"value_b64,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Masked        bool              `json:"masked,omitempty"`

	// KeySR / ValueSR are populated when the key/value carries the Confluent
	// Schema-Registry wire-format magic byte and a decoder for the cluster
	// could resolve and decode the schema (avro/json_schema). When present,
	// the corresponding Encoding is set to the format ("avro" / "json_schema"
	// / "protobuf") and the rendered field holds JSON-form payload.
	KeySR   *SRDecodedMeta `json:"key_sr,omitempty"`
	ValueSR *SRDecodedMeta `json:"value_sr,omitempty"`
}

// ConsumeFrom selects where to start consuming from.
type ConsumeFrom string

// Possible values for ConsumeFrom.
const (
	FromEnd    ConsumeFrom = "end"    // last N records (default)
	FromStart  ConsumeFrom = "start"  // first N records
	FromOffset ConsumeFrom = "offset" // starting at given offset
)

// ConsumeOptions drives ConsumeMessages.
type ConsumeOptions struct {
	Partition int32       // -1 = all
	Limit     int         // per call cap, hard-capped at 500
	From      ConsumeFrom // default FromEnd
	Offset    int64       // used when From==FromOffset
	Timeout   time.Duration
}

const (
	maxConsumeLimit     = 500
	defaultConsumeLimit = 50

	// balanceBuffer absorbs timestamp-interleaving wobble between partitions
	// when merging "last N" results across multiple partitions. With it, the
	// per-partition tail size is ceil(N/K) + buffer; without it, partitions
	// whose newest record is slightly behind another partition's can be
	// under-represented after the merge+truncate step.
	balanceBuffer = 8
)

// fairShare returns the per-partition tail size for a balanced "last
// limit across partitions" fetch. For single-partition (K=1) calls it
// returns exactly limit (no buffer needed: one partition trivially
// preserves order). For zero or negative partitions/limit it returns 0.
func fairShare(limit, partitions int) int {
	if partitions <= 0 || limit <= 0 {
		return 0
	}
	if partitions == 1 {
		return limit
	}
	return ((limit + partitions - 1) / partitions) + balanceBuffer
}

// ConsumeMessages pulls up to opts.Limit messages from the given topic on
// the named cluster using a short-lived kgo.Client.
func (r *Registry) ConsumeMessages(ctx context.Context, cluster, topic string, opts ConsumeOptions) ([]Message, error) {
	cfg, ok := r.clusters[cluster]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, cluster)
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultConsumeLimit
	}
	if opts.Limit > maxConsumeLimit {
		opts.Limit = maxConsumeLimit
	}
	if opts.From == "" {
		opts.From = FromEnd
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}

	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	admCtx, admCancel := context.WithTimeout(ctx, 3*time.Second)
	defer admCancel()

	md, err := adm.Metadata(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata for topic %q on cluster %q: %w", topic, cluster, err)
	}
	t, ok := md.Topics[topic]
	if !ok || t.Err != nil {
		return nil, fmt.Errorf("topic %q not found on cluster %q", topic, cluster)
	}

	parts := make([]int32, 0, len(t.Partitions))
	if opts.Partition >= 0 {
		found := false
		for _, p := range t.Partitions {
			if p.Partition == opts.Partition {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("partition %d not found in topic %q on cluster %q", opts.Partition, topic, cluster)
		}
		parts = append(parts, opts.Partition)
	} else {
		for _, p := range t.Partitions {
			parts = append(parts, p.Partition)
		}
	}

	starts, err := adm.ListStartOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list start offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	ends, err := adm.ListEndOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list end offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}

	// Build per-partition start offsets and total expected message count.
	partOffsets := make(map[int32]kgo.Offset, len(parts))
	expected := 0
	for _, p := range parts {
		startOff := int64(0)
		endOff := int64(0)
		if so, ok := starts.Lookup(topic, p); ok {
			startOff = so.Offset
		}
		if eo, ok := ends.Lookup(topic, p); ok {
			endOff = eo.Offset
		}
		avail := endOff - startOff
		if avail <= 0 {
			continue
		}

		var begin int64
		switch opts.From {
		case FromStart:
			begin = startOff
		case FromOffset:
			begin = opts.Offset
			if begin < startOff {
				begin = startOff
			}
			if begin >= endOff {
				continue
			}
		default: // FromEnd
			// Take last opts.Limit records per partition (capped by availability).
			tail := int64(opts.Limit)
			if avail < tail {
				tail = avail
			}
			begin = endOff - tail
			if begin < startOff {
				begin = startOff
			}
		}
		want := endOff - begin
		if want <= 0 {
			continue
		}
		if want > int64(opts.Limit) {
			want = int64(opts.Limit)
		}
		expected += int(want)
		partOffsets[p] = kgo.NewOffset().At(begin)
	}

	if len(partOffsets) == 0 || expected == 0 {
		return []Message{}, nil
	}
	if expected > opts.Limit {
		expected = opts.Limit
	}

	consumeOpts := clientOpts(cfg, r.log.With("cluster", cluster, "role", "consume"))
	consumeOpts = append(consumeOpts,
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: partOffsets}),
		kgo.FetchMaxWait(500*time.Millisecond),
	)

	cl, err := kgo.NewClient(consumeOpts...)
	if err != nil {
		return nil, fmt.Errorf("create consume client for topic %q on cluster %q: %w", topic, cluster, err)
	}
	defer cl.Close()

	out := make([]Message, 0, expected)
	pollCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	policy := r.MaskingPolicy(cluster)
	dec := r.srDecoderFor(cluster)
	for len(out) < opts.Limit {
		fetches := cl.PollFetches(pollCtx)
		if errs := fetches.Errors(); len(errs) > 0 {
			// Deadline is expected when we've drained partitions without reaching limit.
			if errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
				break
			}
			for _, e := range errs {
				if errors.Is(e.Err, context.Canceled) || errors.Is(e.Err, context.DeadlineExceeded) {
					continue
				}
				return out, fmt.Errorf("fetch topic %q partition %d on cluster %q: %w", topic, e.Partition, cluster, e.Err)
			}
		}
		empty := true
		fetches.EachRecord(func(rec *kgo.Record) {
			if len(out) >= opts.Limit {
				return
			}
			empty = false
			m := recordToMessage(rec)
			m.applySRDecoder(ctx, dec, rec.Key, rec.Value)
			if !policy.IsEmpty() && m.Value != "" {
				if mv, did := policy.Apply(topic, m.Value); did {
					m.Value = mv
					m.Masked = true
				}
			}
			out = append(out, m)
		})
		if empty && pollCtx.Err() != nil {
			break
		}
		if empty {
			break
		}
	}

	return out, nil
}

func recordToMessage(rec *kgo.Record) Message {
	m := Message{
		Partition: rec.Partition,
		Offset:    rec.Offset,
		Timestamp: rec.Timestamp.UnixMilli(),
	}
	m.Key, m.KeyEncoding, m.KeyB64 = decodeBytes(rec.Key)
	m.Value, m.ValueEncoding, m.ValueB64 = decodeBytes(rec.Value)
	if len(rec.Headers) > 0 {
		m.Headers = make(map[string]string, len(rec.Headers))
		for _, h := range rec.Headers {
			if utf8.Valid(h.Value) {
				m.Headers[h.Key] = string(h.Value)
			} else {
				m.Headers[h.Key] = "0x" + hex.EncodeToString(h.Value)
			}
		}
	}
	return m
}

// applySRDecoder runs the optional Schema-Registry decoder over key+value of m.
// When decoding succeeds, the rendered string + encoding are overwritten with
// the JSON form and the corresponding *SR meta is attached. When decoding
// fails, the raw render is kept and meta is still attached so the UI can show
// "schema id N (decode error)".
func (m *Message) applySRDecoder(ctx context.Context, dec *SRDecoder, rawKey, rawValue []byte) {
	if dec == nil {
		return
	}
	if rendered, meta, ok, _ := dec.Decode(ctx, rawKey); meta.Format != "" {
		if ok {
			m.Key = rendered
			m.KeyEncoding = meta.Format
			m.KeyB64 = ""
		}
		mm := meta
		m.KeySR = &mm
	}
	if rendered, meta, ok, _ := dec.Decode(ctx, rawValue); meta.Format != "" {
		if ok {
			m.Value = rendered
			m.ValueEncoding = meta.Format
			m.ValueB64 = ""
		}
		mm := meta
		m.ValueSR = &mm
	}
}

// decodeBytes detects json/text/binary and returns a rendered string plus encoding.
// For binary payloads, it returns a hex preview and full base64 in b64.
func decodeBytes(b []byte) (rendered, encoding, b64 string) {
	if b == nil {
		return "", "null", ""
	}
	if len(b) == 0 {
		return "", "empty", ""
	}
	if utf8.Valid(b) {
		trimmed := bytesTrimSpace(b)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed) {
			return string(b), "json", ""
		}
		return string(b), "text", ""
	}
	preview := b
	if len(preview) > 64 {
		preview = preview[:64]
	}
	return "0x" + hex.EncodeToString(preview), "binary", base64.StdEncoding.EncodeToString(b)
}

func bytesTrimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
