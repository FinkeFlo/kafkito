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
	"sort"
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
	FromEnd       ConsumeFrom = "end"       // last N records (default)
	FromStart     ConsumeFrom = "start"     // first N records
	FromOffset    ConsumeFrom = "offset"    // starting at given offset(s)
	FromTimestamp ConsumeFrom = "timestamp" // starting at first record at-or-after FromTSMs
)

// ConsumeOptions drives ConsumeMessages.
type ConsumeOptions struct {
	Partition int32       // -1 = all partitions
	Limit     int         // per-page cap, hard-capped at 500
	From      ConsumeFrom // default FromEnd
	Offset    int64       // used when From==FromOffset and PartitionOffsets is empty (single partition)

	// PartitionOffsets is the explicit per-partition seek map. When non-empty,
	// it overrides the single-partition Offset field. Used by from=offset for
	// multi-partition seeks and by forward cursor pagination.
	PartitionOffsets map[int32]int64

	// CursorUpperBounds, when non-nil, replaces each partition's
	// high-watermark with the given exclusive upper offset. Used by backward
	// cursor pagination ("the next page is everything strictly older than
	// these offsets per partition"). Only honored when From==FromEnd.
	CursorUpperBounds map[int32]int64

	// FromTSMs / ToTSMs are UNIX millis. Both zero means "no time filter".
	// When set, From should be FromTimestamp.
	FromTSMs int64
	ToTSMs   int64

	Timeout time.Duration
}

// ConsumeResult bundles a page of records with optional continuation.
type ConsumeResult struct {
	Messages   []Message
	NextCursor *Cursor // nil when there are no more records in the current direction
	HasMore    bool
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

// pageWindow is the per-partition [begin, stop) offset range a single
// page of ConsumeMessages will scan.
type pageWindow struct {
	begin int64 // inclusive
	stop  int64 // exclusive
}

// ConsumeMessages pulls up to opts.Limit messages from the named topic
// using a short-lived kgo.Client. For multi-partition (-1) calls with
// from=end, each non-empty partition gets a fair share (ceil(limit/K)
// + buffer) of records, then the merged result is sorted newest-first
// by timestamp and truncated to limit. Single-partition calls and
// forward calls (from=start, from=offset, from=timestamp) bypass the
// fair-share math and consume up to limit records straight.
//
// The returned ConsumeResult.NextCursor, when non-nil, encodes the
// per-partition boundary offsets for the next page in the same
// direction. Callers may pass this value through DecodeCursor and into
// ConsumeOptions.PartitionOffsets / CursorUpperBounds to fetch the
// next page.
func (r *Registry) ConsumeMessages(ctx context.Context, cluster, topic string, opts ConsumeOptions) (*ConsumeResult, error) {
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
		if opts.FromTSMs > 0 || opts.ToTSMs > 0 {
			opts.From = FromTimestamp
		} else {
			opts.From = FromEnd
		}
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

	allParts := make([]int32, 0, len(t.Partitions))
	for _, p := range t.Partitions {
		allParts = append(allParts, p.Partition)
	}
	sort.Slice(allParts, func(i, j int) bool { return allParts[i] < allParts[j] })

	parts := allParts
	if opts.Partition >= 0 {
		found := false
		for _, p := range allParts {
			if p == opts.Partition {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("partition %d not found in topic %q on cluster %q", opts.Partition, topic, cluster)
		}
		parts = []int32{opts.Partition}
	}

	starts, err := adm.ListStartOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list start offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	ends, err := adm.ListEndOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list end offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	startMap := make(map[int32]int64, len(parts))
	endMap := make(map[int32]int64, len(parts))
	for _, p := range parts {
		if so, ok := starts.Lookup(topic, p); ok {
			startMap[p] = so.Offset
		}
		if eo, ok := ends.Lookup(topic, p); ok {
			endMap[p] = eo.Offset
		}
	}

	// FromTimestamp needs an admin call before the offset window math; resolve
	// here so buildWindows can stay pure (offset-only).
	var fromOff, toOff map[int32]int64
	if opts.From == FromTimestamp {
		var rerr error
		fromOff, toOff, rerr = resolveTimestampOffsets(admCtx, adm, topic, parts, opts.FromTSMs, opts.ToTSMs)
		if rerr != nil {
			return nil, fmt.Errorf("resolve time range for topic %q on cluster %q: %w", topic, cluster, rerr)
		}
	}

	windows, err := buildWindows(parts, opts, startMap, endMap, fromOff, toOff)
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 {
		return &ConsumeResult{Messages: []Message{}}, nil
	}

	direction := CursorBackward
	if opts.From == FromStart || opts.From == FromOffset || opts.From == FromTimestamp {
		direction = CursorForward
	}

	partOffsets := make(map[int32]kgo.Offset, len(windows))
	for p, w := range windows {
		partOffsets[p] = kgo.NewOffset().At(w.begin)
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

	collected := make(map[int32][]Message, len(windows))
	totalForward := 0

	pollCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	policy := r.MaskingPolicy(cluster)
	dec := r.srDecoderFor(cluster)

	enough := func() bool {
		if direction == CursorForward {
			return totalForward >= opts.Limit
		}
		// Backward: every active partition must have collected its window
		// (window-size = stop - begin, which is bounded by the fair share).
		for p, w := range windows {
			got := int64(len(collected[p]))
			want := w.stop - w.begin
			if got < want {
				return false
			}
		}
		return true
	}

	emptyStreak := 0
	for !enough() {
		fetches := cl.PollFetches(pollCtx)
		if errs := fetches.Errors(); len(errs) > 0 {
			if errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
				break
			}
			fatal := false
			var firstFatal error
			for _, e := range errs {
				if errors.Is(e.Err, context.Canceled) || errors.Is(e.Err, context.DeadlineExceeded) {
					continue
				}
				if firstFatal == nil {
					firstFatal = fmt.Errorf("fetch topic %q partition %d on cluster %q: %w", topic, e.Partition, cluster, e.Err)
				}
				fatal = true
			}
			if fatal {
				return nil, firstFatal
			}
		}
		if pollCtx.Err() != nil {
			break
		}

		empty := fetches.Empty()
		fetches.EachRecord(func(rec *kgo.Record) {
			w, active := windows[rec.Partition]
			if !active {
				return
			}
			if rec.Offset < w.begin || rec.Offset >= w.stop {
				return
			}
			m := recordToMessage(rec)
			m.applySRDecoder(ctx, dec, rec.Key, rec.Value)
			if !policy.IsEmpty() && m.Value != "" {
				if mv, did := policy.Apply(topic, m.Value); did {
					m.Value = mv
					m.Masked = true
				}
			}
			collected[rec.Partition] = append(collected[rec.Partition], m)
			if direction == CursorForward {
				totalForward++
			}
		})

		if empty {
			emptyStreak++
			// Two consecutive empty polls almost always means we've drained the
			// brokers' assigned partitions. Bail.
			if emptyStreak >= 2 {
				break
			}
		} else {
			emptyStreak = 0
		}
	}

	merged := make([]Message, 0)
	for _, msgs := range collected {
		merged = append(merged, msgs...)
	}
	if direction == CursorBackward {
		sort.Slice(merged, func(i, j int) bool {
			if merged[i].Timestamp != merged[j].Timestamp {
				return merged[i].Timestamp > merged[j].Timestamp
			}
			if merged[i].Partition != merged[j].Partition {
				return merged[i].Partition < merged[j].Partition
			}
			return merged[i].Offset > merged[j].Offset
		})
	} else {
		sort.Slice(merged, func(i, j int) bool {
			if merged[i].Timestamp != merged[j].Timestamp {
				return merged[i].Timestamp < merged[j].Timestamp
			}
			if merged[i].Partition != merged[j].Partition {
				return merged[i].Partition < merged[j].Partition
			}
			return merged[i].Offset < merged[j].Offset
		})
	}
	if len(merged) > opts.Limit {
		merged = merged[:opts.Limit]
	}

	nextCursor, hasMore := buildNextCursor(direction, windows, merged, startMap, endMap)

	return &ConsumeResult{
		Messages:   merged,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

// buildWindows resolves the [begin, stop) offset range to scan per partition,
// based on the From/Offset/PartitionOffsets/CursorUpperBounds fields of opts.
// For FromTimestamp the caller pre-resolves fromOff/toOff via
// resolveTimestampOffsets and passes them through.
func buildWindows(
	parts []int32,
	opts ConsumeOptions,
	startMap, endMap map[int32]int64,
	fromOff, toOff map[int32]int64,
) (map[int32]pageWindow, error) {
	windows := make(map[int32]pageWindow, len(parts))

	switch opts.From {
	case FromTimestamp:
		for _, p := range parts {
			b := startMap[p]
			e := endMap[p]
			if e <= b {
				continue
			}
			if opts.FromTSMs > 0 {
				if o, ok := fromOff[p]; ok && o > b {
					b = o
				}
			}
			if opts.ToTSMs > 0 {
				if o, ok := toOff[p]; ok && o < e {
					e = o
				}
			}
			if e <= b {
				continue
			}
			windows[p] = pageWindow{begin: b, stop: e}
		}
	case FromOffset:
		switch {
		case len(opts.PartitionOffsets) > 0:
			for p, off := range opts.PartitionOffsets {
				if !containsPartition(parts, p) {
					continue
				}
				b := off
				if s, ok := startMap[p]; ok && b < s {
					b = s
				}
				e, ok := endMap[p]
				if !ok || e <= b {
					continue
				}
				windows[p] = pageWindow{begin: b, stop: e}
			}
		case len(parts) == 1:
			p := parts[0]
			b := opts.Offset
			if s, ok := startMap[p]; ok && b < s {
				b = s
			}
			if e, ok := endMap[p]; ok && e > b {
				windows[p] = pageWindow{begin: b, stop: e}
			}
		default:
			return nil, fmt.Errorf("from=offset with partition=-1 requires partition_offsets")
		}
	case FromStart:
		for _, p := range parts {
			s := startMap[p]
			e := endMap[p]
			if e > s {
				windows[p] = pageWindow{begin: s, stop: e}
			}
		}
	default: // FromEnd
		nonEmpty := 0
		for _, p := range parts {
			s := startMap[p]
			e := endMap[p]
			if ub, ok := opts.CursorUpperBounds[p]; ok && ub < e {
				e = ub
			}
			if e > s {
				nonEmpty++
			}
		}
		share := fairShare(opts.Limit, nonEmpty)
		for _, p := range parts {
			s := startMap[p]
			e := endMap[p]
			if ub, ok := opts.CursorUpperBounds[p]; ok && ub < e {
				e = ub
			}
			if e <= s {
				continue
			}
			tail := int64(share)
			if avail := e - s; avail < tail {
				tail = avail
			}
			b := e - tail
			if b < s {
				b = s
			}
			windows[p] = pageWindow{begin: b, stop: e}
		}
	}

	return windows, nil
}

// containsPartition reports whether p appears in parts.
func containsPartition(parts []int32, p int32) bool {
	for _, x := range parts {
		if x == p {
			return true
		}
	}
	return false
}

// buildNextCursor returns the cursor pointing at the next page boundary,
// or (nil, false) when no further records remain in the given direction.
func buildNextCursor(
	direction CursorDirection,
	windows map[int32]pageWindow,
	page []Message,
	startMap, endMap map[int32]int64,
) (*Cursor, bool) {
	if len(page) == 0 {
		return nil, false
	}
	c := Cursor{
		Direction:  direction,
		Partitions: make(map[int32]int64, len(windows)),
	}
	hasMore := false
	switch direction {
	case CursorBackward:
		// Lowest offset seen per partition on this page; the next page
		// consumes records with offset strictly less than that boundary.
		lowest := make(map[int32]int64, len(windows))
		for _, m := range page {
			cur, ok := lowest[m.Partition]
			if !ok || m.Offset < cur {
				lowest[m.Partition] = m.Offset
			}
		}
		for p, w := range windows {
			lo, ok := lowest[p]
			if !ok {
				lo = w.begin
			}
			if lo > startMap[p] {
				hasMore = true
			}
			c.Partitions[p] = lo
		}
	default: // CursorForward
		highest := make(map[int32]int64, len(windows))
		for _, m := range page {
			cur, ok := highest[m.Partition]
			if !ok || m.Offset > cur {
				highest[m.Partition] = m.Offset
			}
		}
		for p, w := range windows {
			hi, ok := highest[p]
			next := hi + 1
			if !ok {
				next = w.begin
			}
			if next < w.stop && next < endMap[p] {
				hasMore = true
			}
			c.Partitions[p] = next
		}
	}
	if !hasMore {
		return nil, false
	}
	return &c, true
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
