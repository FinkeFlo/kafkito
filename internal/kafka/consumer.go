// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

	// HeadersB64 carries the standard-base64 raw bytes of header values that are
	// not valid UTF-8. Those keys still appear in Headers with a "0x…" hex
	// rendering for display; HeadersB64 is what a byte-for-byte reproduction
	// (see Registry.Produce) must use instead.
	HeadersB64 map[string]string `json:"headers_b64,omitempty"`

	Masked bool `json:"masked,omitempty"`

	// ValueSizeBytes is the untruncated byte length of the raw Kafka value.
	// Populated whenever the value was read; zero means the record had no
	// value (nil/empty).
	ValueSizeBytes int64 `json:"value_size_bytes,omitempty"`
	// ValueTruncated is true when Value (and ValueB64) were cut to
	// maxMessageValueBytes. The full payload is on the broker; the UI should
	// surface this so the user knows they are seeing a preview only.
	ValueTruncated bool `json:"value_truncated,omitempty"`

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

	// Partial is true when a from=end page could not fully collect its
	// intended tail window before ConsumeOptions.Timeout elapsed (e.g. a
	// very large record ahead of it in offset order stalled the transfer).
	// The window is exactly sized to the available records (see
	// buildWindows), so this only fires on a genuine fetch problem, never
	// on legitimately running out of history. Callers should surface this
	// so "latest" pages don't silently pass off an incomplete tail as
	// complete — see the "partial" field of the consumeMessages response.
	Partial bool
}

const (
	maxConsumeLimit     = 500
	defaultConsumeLimit = 50

	// maxMessageValueBytes caps the rendered value string stored in Message.Value
	// and the raw bytes passed to decodeBytes. Payloads larger than this are
	// truncated before string allocation; Message.ValueTruncated is set to true
	// and Message.ValueSizeBytes carries the original byte length so the UI can
	// show a "preview only" indicator.
	maxMessageValueBytes = 64 * 1024 // 64 KB

	// binaryPreviewBytes is how many leading bytes of a non-UTF-8 payload are
	// rendered as the "0x…" hex preview shown in place of the value.
	binaryPreviewBytes = 64

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
	begin     int64 // inclusive fetch begin for this page
	stop      int64 // exclusive fetch stop for this page
	trueBegin int64 // inclusive lower bound of the full query range
}

// ConsumeMessages pulls up to opts.Limit messages from the named topic
// using a short-lived kgo.Client. For multi-partition (-1) calls with
// from=end, each non-empty partition gets a fair share (ceil(limit/K)
// + buffer) of records, then the merged result is sorted newest-first
// by timestamp and truncated to limit. Forward calls (from=start,
// from=offset, from=timestamp) bypass the fair-share math: they read on
// until every partition is drained or has read past the page's last
// record, then merge oldest-first and truncate to limit.
//
// The returned ConsumeResult.NextCursor, when non-nil, encodes the
// per-partition boundary offsets for the next page in the same
// direction. Callers may pass this value through DecodeCursor and into
// ConsumeOptions.PartitionOffsets / CursorUpperBounds to fetch the
// next page.
func (r *Registry) ConsumeMessages(ctx context.Context, cluster, topic string, opts ConsumeOptions) (*ConsumeResult, error) {
	cfg, ok := r.ConfigFor(cluster)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, cluster)
	}
	opts = opts.withDefaults()

	// Time bounds are resolved up front so buildWindows can stay pure
	// (offset-only). They apply to FromTimestamp (the seek mechanism), to
	// FromEnd / FromStart (where they clamp the browse window) and cap
	// forward cursor pages.
	offs, err := r.readerOffsets(ctx, cluster, topic, offsetsQuery{
		partition: opts.Partition, fromTSMs: opts.FromTSMs, toTSMs: opts.ToTSMs,
	})
	if err != nil {
		return nil, err
	}
	windows, err := buildWindows(offs.parts, opts, offs.start, offs.end, offs.fromTS, offs.toTS)
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 {
		return &ConsumeResult{Messages: []Message{}}, nil
	}

	direction := opts.direction()
	deadline := time.Now().Add(opts.Timeout)
	scan := recordScan{cluster: cluster, topic: topic, role: "consume", cfg: cfg, drainAfter: 2}
	collected, err := r.collectWindows(ctx, deadline, scan, opts, windows)
	if err != nil {
		return nil, err
	}
	if direction == CursorBackward && windowsFull(windows, collected) {
		if err := r.refillCappedWindows(ctx, deadline, scan, opts, windows, collected); err != nil {
			return nil, err
		}
	}
	merged := mergePage(collected, direction == CursorBackward, opts.Limit)

	prev := opts.CursorUpperBounds
	if direction == CursorForward {
		prev = opts.PartitionOffsets
	}
	nextCursor, hasMore := buildNextCursor(direction, windows, merged, collected, prev)

	return &ConsumeResult{
		Messages:   merged,
		NextCursor: nextCursor,
		HasMore:    hasMore,
		Partial:    direction == CursorBackward && !windowsFull(windows, collected),
	}, nil
}

func (o ConsumeOptions) withDefaults() ConsumeOptions {
	if o.Limit <= 0 {
		o.Limit = defaultConsumeLimit
	}
	o.Limit = min(o.Limit, maxConsumeLimit)
	if o.From == "" {
		o.From = FromEnd
		if o.FromTSMs > 0 || o.ToTSMs > 0 {
			o.From = FromTimestamp
		}
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	return o
}

func (o ConsumeOptions) direction() CursorDirection {
	switch o.From {
	case FromStart, FromOffset, FromTimestamp:
		return CursorForward
	default: // FromEnd, like buildWindows
		return CursorBackward
	}
}

// collectWindows reads the page windows and returns the decoded records per
// partition. It stops once the page can be cut (forward: see
// forwardPageSettled; backward: every window complete), the windows are
// drained or the deadline passes; a timeout is not an error, the page is
// then just short.
func (r *Registry) collectWindows(ctx context.Context, deadline time.Time, s recordScan, opts ConsumeOptions, windows map[int32]pageWindow) (map[int32][]Message, error) {
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	s.ranges = windowRanges(windows)
	dec := r.recordDecoder(s.cluster, s.topic)
	collected := make(map[int32][]Message, len(windows))
	drained := make(map[int32]bool, len(windows))
	forward := opts.direction() == CursorForward
	for batch, err := range r.scanRecords(pollCtx, s) {
		if isContextErr(err) {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, rec := range batch.records {
			collected[rec.Partition] = append(collected[rec.Partition], dec.message(ctx, rec))
		}
		for _, p := range batch.drained {
			drained[p] = true
		}
		if forward && forwardPageSettled(windows, collected, drained, opts.Limit) || !forward && windowsFull(windows, collected) {
			break
		}
	}
	return collected, nil
}

// forwardPageSettled reports whether an oldest-first page can be cut: every
// window is drained or has already read past the page's last record, so no
// unread record can rank inside the page. Polls may return one partition's
// records long before another's, so the page size alone is not enough.
//
// A partition's records arrive in offset order and, as everywhere in the
// merge, timestamps are taken as non-decreasing within a partition, so its
// next unread record ranks at or after {last timestamp, p, last offset + 1}.
func forwardPageSettled(windows map[int32]pageWindow, collected map[int32][]Message, drained map[int32]bool, limit int) bool {
	var page []Message
	for p := range windows {
		if drained[p] {
			continue
		}
		got := collected[p]
		if len(got) == 0 {
			return false
		}
		if page == nil {
			page = mergePage(collected, false, limit)
		}
		last := got[len(got)-1]
		next := Message{Timestamp: last.Timestamp, Partition: p, Offset: last.Offset + 1}
		if len(page) < limit || compareMessages(next, page[len(page)-1], false) < 0 {
			return false
		}
	}
	return true
}

// refillCappedWindows widens the from=end windows that the fair share may
// have cut short and reads the extra records into collected.
//
// A capped partition's unread records are all older than its oldest
// collected one, so they can only matter if a record just below the window
// would still rank inside the page. Widening such a window to limit records
// always suffices, because no page holds more than limit records of one
// partition, so a single round is enough.
func (r *Registry) refillCappedWindows(ctx context.Context, deadline time.Time, s recordScan, opts ConsumeOptions, windows map[int32]pageWindow, collected map[int32][]Message) error {
	page := mergePage(collected, true, opts.Limit)
	extra := make(map[int32]pageWindow)
	for p, w := range windows {
		got := collected[p]
		if w.begin <= w.trueBegin || len(got) == 0 {
			continue
		}
		below := Message{Timestamp: got[0].Timestamp, Partition: p, Offset: w.begin - 1}
		if len(page) == opts.Limit && compareMessages(below, page[len(page)-1], true) > 0 {
			continue
		}
		extra[p] = pageWindow{begin: max(w.trueBegin, w.begin-int64(opts.Limit-len(got))), stop: w.begin}
	}
	if len(extra) == 0 {
		return nil
	}
	more, err := r.collectWindows(ctx, deadline, s, opts, extra)
	if err != nil {
		return err
	}
	for p, e := range extra {
		w := windows[p]
		w.begin = e.begin
		windows[p] = w
		collected[p] = append(more[p], collected[p]...)
	}
	return nil
}

// mergePage merges the collected records, sorts them and cuts to limit.
func mergePage(collected map[int32][]Message, newestFirst bool, limit int) []Message {
	merged := make([]Message, 0)
	for _, msgs := range collected {
		merged = append(merged, msgs...)
	}
	sortMessages(merged, newestFirst)
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// windowsFull reports whether every window has collected all its records
// (window size = stop - begin, which is bounded by the fair share).
func windowsFull(windows map[int32]pageWindow, collected map[int32][]Message) bool {
	for p, w := range windows {
		if int64(len(collected[p])) < w.stop-w.begin {
			return false
		}
	}
	return true
}

func windowRanges(windows map[int32]pageWindow) map[int32]PartitionRange {
	out := make(map[int32]PartitionRange, len(windows))
	for p, w := range windows {
		out[p] = PartitionRange{Start: w.begin, End: w.stop}
	}
	return out
}

// buildWindows resolves the [begin, stop) offset range to scan per partition,
// based on the From/Offset/PartitionOffsets/CursorUpperBounds fields of opts.
// fromOff/toOff are the offsets of the time bounds as resolved by
// resolveTimestampOffsets, nil when the respective bound is unset.
func buildWindows(
	parts []int32,
	opts ConsumeOptions,
	startMap, endMap map[int32]int64,
	fromOff, toOff map[int32]int64,
) (map[int32]pageWindow, error) {
	windows := make(map[int32]pageWindow, len(parts))
	add := func(p int32, b, e int64) {
		if e > b {
			windows[p] = pageWindow{begin: b, stop: e, trueBegin: b}
		}
	}

	switch opts.From {
	case FromTimestamp, FromStart:
		for _, p := range parts {
			b, e := clampRange(p, startMap[p], endMap[p], fromOff, toOff)
			add(p, b, e)
		}
	case FromOffset:
		// A forward cursor continues a time-range query, so the exclusive
		// upper time bound still applies to partition_offsets.
		offsets, upper := opts.PartitionOffsets, toOff
		if len(offsets) == 0 {
			if len(parts) != 1 {
				return nil, fmt.Errorf("from=offset with partition=-1 requires partition_offsets")
			}
			offsets, upper = map[int32]int64{parts[0]: opts.Offset}, nil
		}
		for p, b := range offsets {
			e, ok := endMap[p]
			if !ok || !slices.Contains(parts, p) {
				continue
			}
			if s, ok := startMap[p]; ok {
				b = max(b, s)
			}
			_, e = clampRange(p, b, e, nil, upper)
			add(p, b, e)
		}
	default: // FromEnd
		// Clamp order: time bounds (filter window), then cursor upper bound
		// (paginate within filter window). Partitions whose entire range
		// falls outside drop out of the fair-share denominator.
		ranges := make(map[int32]PartitionRange, len(parts))
		for _, p := range parts {
			s, e := clampRange(p, startMap[p], endMap[p], fromOff, toOff)
			_, e = clampRange(p, s, e, nil, opts.CursorUpperBounds)
			if e > s {
				ranges[p] = PartitionRange{Start: s, End: e}
			}
		}
		share := int64(fairShare(opts.Limit, len(ranges)))
		for p, rng := range ranges {
			windows[p] = pageWindow{begin: max(rng.Start, rng.End-share), stop: rng.End, trueBegin: rng.Start}
		}
	}
	return windows, nil
}

// clampRange narrows [start, end) of partition p to the offsets in fromOff
// (inclusive lower) and toOff (exclusive upper); nil maps leave it as is.
func clampRange(p int32, start, end int64, fromOff, toOff map[int32]int64) (int64, int64) {
	if o, ok := fromOff[p]; ok && o > start {
		start = o
	}
	if o, ok := toOff[p]; ok && o < end {
		end = o
	}
	return start, end
}

// buildNextCursor returns the cursor pointing at the next page boundary,
// or (nil, false) when no further records remain in the given direction.
//
// For backward paging, "more" is judged against the full query lower bound
// (w.trueBegin), not the page-local fetch begin. This lets from=end continue
// paging older history after draining a fair-share tail, while still stopping
// at the true start of the query range (including time-window clamps).
// Forward paging continues to compare against the clamped page stop.
//
// collected holds the pre-truncation records fetched per partition for this
// page. Backward paging needs it to tell "this partition's records were
// dropped by the limit truncation" (boundary must stay at the window stop so
// the next page re-fetches them) apart from "this partition's window was
// empty" (boundary advances toward trueBegin). Without that distinction a
// fully-truncated partition would advance its boundary past its own fetched
// window and silently skip those records.
//
// prev holds the incoming per-partition boundaries (CursorUpperBounds for
// backward, PartitionOffsets for forward). Boundaries for partitions that have
// dropped out of the active window set (exhausted or excluded by a time clamp)
// are carried forward unchanged so a later page — driven by another partition
// still paging — cannot resurrect them and re-deliver their records.
func buildNextCursor(
	direction CursorDirection,
	windows map[int32]pageWindow,
	page []Message,
	collected map[int32][]Message,
	prev map[int32]int64,
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
			if lo, ok := lowest[p]; ok {
				// Partition contributed to this page: the next page reads
				// strictly below the lowest delivered offset.
				if lo > w.trueBegin {
					hasMore = true
				}
				c.Partitions[p] = lo
				continue
			}
			if len(collected[p]) > 0 {
				// Records were fetched for this partition but dropped by the
				// limit truncation. Hold the boundary at the window stop so the
				// next page re-fetches [.., stop) instead of skipping them.
				c.Partitions[p] = w.stop
				hasMore = true
				continue
			}
			// Empty window (e.g. compacted away): page further back toward the
			// true query lower bound.
			c.Partitions[p] = w.begin
			if w.begin > w.trueBegin {
				hasMore = true
			}
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
			if next < w.stop {
				hasMore = true
			}
			c.Partitions[p] = next
		}
	}
	// Carry forward boundaries for partitions no longer in the active window
	// set so a later page cannot re-fetch them from scratch.
	for p, off := range prev {
		if _, active := windows[p]; !active {
			if _, set := c.Partitions[p]; !set {
				c.Partitions[p] = off
			}
		}
	}
	if !hasMore {
		return nil, false
	}
	return &c, true
}

// MaxRawDownloadMB is the download cap in megabytes, exported so handlers can
// include it in error messages.
const MaxRawDownloadMB = 15

// maxRawDownloadBytes is the per-request cap for the raw message value
// download endpoint. Requests for values larger than this are rejected with
// 413 so a single oversized record cannot exhaust process memory.
const maxRawDownloadBytes = MaxRawDownloadMB * 1024 * 1024

// RawMessageValue is the result of FetchRawMessageValue.
type RawMessageValue struct {
	Value       []byte
	ContentType string // "application/json", "text/plain", or "application/octet-stream"
	Extension   string // suggested file extension without leading dot
}

// FetchRawMessageValue fetches the raw value bytes of a single Kafka record
// identified by cluster, topic, partition, and offset. It never builds a
// Message struct or performs any string/base64 conversion, so it is safe to
// call for records larger than maxMessageValueBytes as long as the payload
// stays within maxRawDownloadBytes.
//
// Returns ErrValueTooLarge when the record's value exceeds maxRawDownloadBytes
// and ErrValueMasked when the cluster's masking policy changes the record's
// value: the raw bytes would bypass the masking.
func (r *Registry) FetchRawMessageValue(ctx context.Context, cluster, topic string, partition int32, offset int64) (*RawMessageValue, error) {
	cfg, ok := r.ConfigFor(cluster)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, cluster)
	}

	var rec *kgo.Record
	for batch, err := range r.scanRecords(ctx, recordScan{
		cluster: cluster, topic: topic, role: "raw-download", cfg: cfg,
		ranges: map[int32]PartitionRange{partition: {Start: offset, End: offset + 1}}, drainAfter: 1,
	}) {
		if errors.Is(err, context.Canceled) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("poll fetches: %w", err)
		}
		if len(batch.records) > 0 {
			rec = batch.records[0]
		}
	}
	if rec == nil {
		return nil, fmt.Errorf("record not found: partition %d offset %d", partition, offset)
	}

	if int64(len(rec.Value)) > maxRawDownloadBytes {
		return nil, ErrValueTooLarge
	}
	if r.recordDecoder(cluster, topic).valueMasked(ctx, rec) {
		return nil, ErrValueMasked
	}

	ct, ext := detectContentType(rec.Value)
	return &RawMessageValue{
		Value:       rec.Value,
		ContentType: ct,
		Extension:   ext,
	}, nil
}

// ErrValueTooLarge is returned when a raw value exceeds maxRawDownloadBytes.
var ErrValueTooLarge = errors.New("value exceeds download size limit")

// ErrValueMasked is returned when the raw value of a masked record is
// requested.
var ErrValueMasked = errors.New("value is masked and cannot be downloaded")

// detectContentType returns a MIME type and file extension for raw Kafka value
// bytes. JSON and UTF-8 text are distinguished from binary payloads.
func detectContentType(b []byte) (mimeType, ext string) {
	if len(b) == 0 {
		return "application/octet-stream", "bin"
	}
	if utf8.Valid(b) {
		trimmed := bytesTrimSpace(b)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed) {
			return "application/json", "json"
		}
		return "text/plain; charset=utf-8", "txt"
	}
	return "application/octet-stream", "bin"
}
