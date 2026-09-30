// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/antchfx/xpath"
	"github.com/ohler55/ojg/jp"
	"github.com/twmb/franz-go/pkg/kgo"
)

// SearchMode selects the predicate family used while scanning.
type SearchMode string

// Supported search modes.
const (
	SearchModeContains SearchMode = "contains"
	SearchModeJSONPath SearchMode = "jsonpath"
	SearchModeXPath    SearchMode = "xpath"
	SearchModeJS       SearchMode = "js"
)

// SearchOp enumerates the comparison operators for path modes.
type SearchOp string

// Supported search operators.
const (
	OpExists   SearchOp = "exists"
	OpEq       SearchOp = "eq"
	OpNeq      SearchOp = "ne"
	OpContains SearchOp = "contains"
	OpRegex    SearchOp = "regex"
	OpGt       SearchOp = "gt"
	OpLt       SearchOp = "lt"
	OpGte      SearchOp = "gte"
	OpLte      SearchOp = "lte"
)

// SearchZone names the record part to consider for contains-mode matching.
// Path modes always operate on the record value.
type SearchZone string

// Search zones.
const (
	ZoneValue   SearchZone = "value"
	ZoneKey     SearchZone = "key"
	ZoneHeaders SearchZone = "headers"
)

// SearchDirection chooses the time ordering for results.
type SearchDirection string

// Search directions.
const (
	DirNewestFirst SearchDirection = "newest_first"
	DirOldestFirst SearchDirection = "oldest_first"
)

// SearchOptions configures SearchMessages.
type SearchOptions struct {
	Partition int32
	Limit     int
	Budget    int
	Direction SearchDirection

	Mode  SearchMode
	Path  string
	Op    SearchOp
	Value string
	Zones []SearchZone

	// FromTS/ToTS are UNIX millis. Zero means "not set"; zero lower bound defaults
	// to the partition start, zero upper bound to the partition end.
	FromTS int64
	ToTS   int64

	// Cursors (per-partition) for continuation. If set they override the begin
	// offsets resolved from FromTS/ToTS.
	Cursors map[int32]int64

	// StopOnLimit returns once Limit matches have been collected; otherwise we
	// scan up to Budget records and return all matches found.
	StopOnLimit bool

	Timeout time.Duration
}

// ParseErrorOffset records the location and reason of a single message that
// could not be parsed during a structured search (JSONPath, XPath, …).
type ParseErrorOffset struct {
	Partition int32  `json:"partition"`
	Offset    int64  `json:"offset"`
	Error     string `json:"error"`
}

// parseErrorOffsetsCap is the maximum number of parse error locations kept per
// search response. It prevents unbounded memory growth on topics with many
// malformed messages.
const parseErrorOffsetsCap = 50

// SearchStats is the per-response summary.
type SearchStats struct {
	Scanned         int  `json:"scanned"`
	Matched         int  `json:"matched"`
	BudgetExhausted bool `json:"budget_exhausted"`
	// TimedOut is true when the scan stopped because the per-call timeout
	// elapsed before the budget or the partition range was exhausted.
	TimedOut bool `json:"timed_out"`
	// MoreAvailable is true when the resolved range was not fully scanned, i.e.
	// a continuation call with NextCursors would scan additional records.
	MoreAvailable bool                     `json:"more_available"`
	Direction     SearchDirection          `json:"direction"`
	NextCursors   map[int32]int64          `json:"next_cursors,omitempty"`
	ResolvedRange map[int32]PartitionRange `json:"resolved_range,omitempty"`
	ParseErrors   int                      `json:"parse_errors"`
	// ParseErrorOffsets lists up to 50 partition/offset pairs where a message
	// could not be parsed. Use this to locate and inspect the raw messages.
	ParseErrorOffsets []ParseErrorOffset `json:"parse_error_offsets,omitempty"`
	Durations         map[string]int64   `json:"durations_ms,omitempty"`
}

// PartitionRange reports the offset window actually scanned on a partition.
type PartitionRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// SearchResult is the value returned by SearchMessages.
type SearchResult struct {
	Messages []Message   `json:"messages"`
	Stats    SearchStats `json:"stats"`
}

// compile turns the options into a matcher. Nil matcher means "pass all".
func (o SearchOptions) compile() (matcher, error) {
	if strings.TrimSpace(o.Value) == "" && o.Op != OpExists {
		// Empty needle with no predicate = no filter.
		return passAllMatcher{}, nil
	}
	zones := o.Zones
	if len(zones) == 0 {
		zones = []SearchZone{ZoneValue}
	}
	switch o.Mode {
	case "", SearchModeContains:
		return &containsMatcher{needle: o.Value, zones: zones}, nil
	case SearchModeJSONPath:
		expr, err := jp.ParseString(o.Path)
		if err != nil {
			return nil, fmt.Errorf("jsonpath %q: %w", o.Path, err)
		}
		return newPathMatcher(jsonPathEval(expr), o.Op, o.Value)
	case SearchModeXPath:
		// o.Path is the caller-authored XPath query itself (like a grep
		// pattern), not user data spliced into a privileged expression -
		// there is no base query for it to escape, and the antchfx/xpath
		// engine used here has no variable-binding API to parameterize
		// against anyway. Length/complexity is bounded by the caller
		// (see maxSearchPathLen in internal/server) to guard against
		// pathological expressions.
		expr, err := xpath.Compile(o.Path)
		if err != nil {
			return nil, fmt.Errorf("xpath %q: %w", o.Path, err)
		}
		return newPathMatcher(xmlPathEval(expr), o.Op, o.Value)
	case SearchModeJS:
		return newJSMatcher(o.Value)
	default:
		return nil, fmt.Errorf("unknown search mode: %s", o.Mode)
	}
}

// resolveSearchRange picks the [start, end) offset range to scan per
// partition from the partition offsets, the time bounds and the
// continuation cursors, which override the end (newest-first) or the start
// (oldest-first).
func resolveSearchRange(opts SearchOptions, offs *topicOffsets) map[int32]PartitionRange {
	out := make(map[int32]PartitionRange, len(offs.parts))
	for _, p := range offs.parts {
		begin, finish := offs.bounds(p)
		if c, ok := opts.Cursors[p]; ok {
			if opts.Direction == DirNewestFirst {
				finish = min(finish, c)
			} else {
				begin = max(begin, c)
			}
		}
		if finish > begin {
			out[p] = PartitionRange{Start: begin, End: finish}
		}
	}
	return out
}

// Search defaults and caps.
const (
	defaultSearchBudget = 10000
	maxSearchBudget     = 500000
	// searchChunkSize is how many offsets per partition a newest-first
	// search reads per backward step at most. Larger values reduce re-seek
	// overhead; smaller values give tighter stop-on-limit responsiveness
	// when matches are dense near the end. A call only advances its cursor
	// over whole chunks, so the chunk also shrinks to the budget's share per
	// partition (see searchChunk).
	searchChunkSize int64 = 4000
)

// SearchMessages scans up to opts.Budget records across the selected partitions
// and returns those matching the compiled predicate. It is a read-only op; it
// never commits offsets.
func (r *Messages) SearchMessages(ctx context.Context, cluster, topic string, opts SearchOptions) (*SearchResult, error) {
	cfg, ok := r.ConfigFor(cluster)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, cluster)
	}
	opts = opts.withDefaults()
	mt, err := opts.compile()
	if err != nil {
		return nil, err
	}

	// Partitions are not validated: an unknown partition resolves to an
	// empty range and therefore to an empty result.
	offs, err := r.readerOffsets(ctx, cluster, topic, offsetsQuery{
		partition: opts.Partition, unchecked: true, fromTSMs: opts.FromTS, toTSMs: opts.ToTS,
	})
	if err != nil {
		return nil, err
	}
	ranges := resolveSearchRange(opts, offs)
	if len(ranges) == 0 {
		return &SearchResult{
			Messages: []Message{},
			Stats: SearchStats{
				Direction:     opts.Direction,
				ResolvedRange: map[int32]PartitionRange{},
			},
		}, nil
	}

	started := time.Now()
	sc := newSearchScan(mt, r.recordDecoder(cluster, topic), ranges, opts.Direction)
	scan := recordScan{cluster: cluster, topic: topic, role: "search", cfg: cfg, ranges: ranges, drainAfter: 1}
	if opts.Direction == DirNewestFirst {
		scan.chunk = searchChunk(opts.Budget, len(ranges))
	}
	budgetExhausted, timedOut, err := r.runSearch(ctx, scan, opts, sc)
	if err != nil {
		return nil, err
	}

	matches := sc.confirmedMatches()
	sortMessages(matches, opts.Direction == DirNewestFirst)
	matched := len(matches)
	if matched > opts.Limit {
		matches = matches[:opts.Limit]
	}
	nextCursors, moreAvailable := sc.continuation(ranges)
	carryDoneCursors(nextCursors, opts.Cursors, offs.parts)

	return &SearchResult{Messages: matches, Stats: SearchStats{
		Scanned:           sc.scanned,
		Matched:           matched,
		BudgetExhausted:   budgetExhausted,
		TimedOut:          timedOut,
		MoreAvailable:     moreAvailable,
		Direction:         opts.Direction,
		NextCursors:       nextCursors,
		ResolvedRange:     ranges,
		ParseErrors:       sc.parseErrors,
		ParseErrorOffsets: sc.parseErrorOffsets,
		Durations:         map[string]int64{"total": time.Since(started).Milliseconds()},
	}}, nil
}

// searchChunk is the chunk size of a newest-first search: searchChunkSize,
// capped to the budget's share per partition so that every call can finish
// at least one chunk and move its cursor.
func searchChunk(budget, partitions int) int64 {
	return max(1, min(searchChunkSize, int64(budget/max(1, partitions))))
}

func (o SearchOptions) withDefaults() SearchOptions {
	if o.Limit <= 0 {
		o.Limit = defaultConsumeLimit
	}
	o.Limit = min(o.Limit, maxConsumeLimit)
	if o.Budget <= 0 {
		o.Budget = defaultSearchBudget
	}
	o.Budget = min(o.Budget, maxSearchBudget)
	if o.Direction == "" {
		o.Direction = DirNewestFirst
	}
	if o.Timeout <= 0 {
		o.Timeout = 8 * time.Second
	}
	return o
}

// runSearch feeds the scanned records into sc until the ranges are drained,
// the budget is spent, enough matches were found (StopOnLimit) or
// opts.Timeout elapses. Budget and limit are checked after every poll.
func (r *Messages) runSearch(ctx context.Context, s recordScan, opts SearchOptions, sc *searchScan) (budgetExhausted, timedOut bool, err error) {
	pollCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	for batch, err := range r.scanRecords(pollCtx, s) {
		if errors.Is(err, context.DeadlineExceeded) {
			return false, true, nil
		}
		if err != nil {
			return false, false, err
		}
		for _, rec := range batch.records {
			sc.visit(ctx, rec)
		}
		sc.finish(batch)
		if sc.scanned >= opts.Budget {
			return true, false, nil
		}
		if opts.StopOnLimit && sc.confirmed >= opts.Limit {
			break
		}
	}
	return false, false, nil
}

// searchScan accumulates the matches and stats of one SearchMessages call
// and tracks, per partition, how far the range was read without a gap.
type searchScan struct {
	match             matcher
	dec               recordDecoder
	newestFirst       bool
	matches           []Message
	scanned           int
	parseErrors       int
	parseErrorOffsets []ParseErrorOffset
	// confirmed counts the matches at or above their partition's frontier,
	// i.e. the matches this call returns (before the limit).
	confirmed int
	// pending counts, per partition, the matches of the chunk being read
	// (newest-first); they are confirmed once the chunk is read to its end.
	pending map[int32]int
	// frontier is, per partition, the lower offset of the lowest chunk read
	// to its end without a gap from the top of the range (newest-first). It
	// starts at the range end.
	frontier map[int32]int64
	// stuck marks the partitions whose walk hit a chunk that was not read to
	// its end (newest-first); their frontier no longer moves.
	stuck map[int32]bool
	// highest is the highest offset processed per partition (oldest-first).
	highest map[int32]int64
	// ended marks the partitions whose range was read to its end.
	ended map[int32]bool
}

func newSearchScan(mt matcher, dec recordDecoder, ranges map[int32]PartitionRange, dir SearchDirection) *searchScan {
	sc := &searchScan{
		match:       mt,
		dec:         dec,
		newestFirst: dir == DirNewestFirst,
		pending:     make(map[int32]int, len(ranges)),
		frontier:    make(map[int32]int64, len(ranges)),
		stuck:       make(map[int32]bool),
		highest:     make(map[int32]int64, len(ranges)),
		ended:       make(map[int32]bool, len(ranges)),
	}
	for p, rng := range ranges {
		sc.frontier[p] = rng.End
	}
	return sc
}

func (sc *searchScan) visit(ctx context.Context, rec *kgo.Record) {
	p := rec.Partition
	if cur, ok := sc.highest[p]; !ok || rec.Offset > cur {
		sc.highest[p] = rec.Offset
	}
	sc.scanned++
	// Match against the full, untruncated record: truncating first would
	// hide contains-matches past maxMessageValueBytes and corrupt
	// JSONPath/XPath/JS parsing of any larger record. Where a masking rule
	// applies, the value is matched in its masked form, so masked content
	// cannot be found by searching for it. A hit is rebuilt through the
	// truncating, masking path so the response carries the same bounded
	// preview as every other consume path.
	full := sc.dec.matchMessage(ctx, rec)
	hit, err := sc.match.match(&full)
	if err != nil {
		sc.parseError(ctx, rec, err)
		return
	}
	if hit {
		sc.matches = append(sc.matches, sc.dec.message(ctx, rec))
		if sc.newestFirst {
			sc.pending[p]++
		} else {
			sc.confirmed++
		}
	}
}

// finish applies what a poll finished: the partitions read to their end and,
// newest-first, the chunks that move a partition's frontier down. A chunk
// the reader gave up on stops the frontier above it for good.
func (sc *searchScan) finish(batch recordBatch) {
	for _, p := range batch.ended {
		sc.ended[p] = true
	}
	if !sc.newestFirst {
		return
	}
	for _, ch := range batch.chunks {
		p := ch.partition
		if sc.stuck[p] {
			continue
		}
		if ch.forced {
			sc.stuck[p] = true
			continue
		}
		sc.frontier[p] = ch.lower
		sc.confirmed += sc.pending[p]
		sc.pending[p] = 0
	}
}

// confirmedMatches returns the matches this call reports. Newest-first, it
// drops the matches below their partition's frontier: they come from a
// chunk that was not read to its end, so the next call, which continues at
// the frontier, reads them again, and keeping them would also break the
// newest-first order across calls.
func (sc *searchScan) confirmedMatches() []Message {
	if !sc.newestFirst {
		return sc.matches
	}
	out := sc.matches[:0]
	for _, m := range sc.matches {
		if m.Offset >= sc.frontier[m.Partition] {
			out = append(out, m)
		}
	}
	return out
}

// parseErrorWithheld replaces the parse error text on topics with an active
// masking rule: parser and JS errors can quote parts of the value.
const parseErrorWithheld = "value could not be evaluated (details withheld: data masking applies to this topic)"

func (sc *searchScan) parseError(ctx context.Context, rec *kgo.Record, err error) {
	sc.parseErrors++
	msg := err.Error()
	if sc.dec.masks {
		msg = parseErrorWithheld
	}
	slog.WarnContext(ctx, "search: skipping message – parse error",
		"partition", rec.Partition,
		"offset", rec.Offset,
		"error", msg)
	if len(sc.parseErrorOffsets) < parseErrorOffsetsCap {
		sc.parseErrorOffsets = append(sc.parseErrorOffsets, ParseErrorOffset{
			Partition: rec.Partition,
			Offset:    rec.Offset,
			Error:     msg,
		})
	}
}

// carryDoneCursors copies the cursors of partitions that an earlier call
// already finished into next. Such a partition resolves to an empty range
// and so gets no cursor of its own; without its cursor a follow-up call
// would search it again from the start.
func carryDoneCursors(next, cursors map[int32]int64, parts []int32) {
	for _, p := range parts {
		if _, ok := next[p]; ok {
			continue
		}
		if c, ok := cursors[p]; ok {
			next[p] = c
		}
	}
}

// continuation returns the per-partition cursors for a follow-up search and
// whether that search would scan anything. A cursor never skips an offset
// this call did not read: only a partition read to its end is done, one
// without any record keeps its whole range.
func (sc *searchScan) continuation(ranges map[int32]PartitionRange) (map[int32]int64, bool) {
	next := make(map[int32]int64, len(ranges))
	more := false
	for p, rng := range ranges {
		if sc.newestFirst {
			// A follow-up scans [Start, frontier): everything above is done.
			next[p] = sc.frontier[p]
			more = more || next[p] > rng.Start
			continue
		}
		switch hi, ok := sc.highest[p]; {
		case sc.ended[p]:
			next[p] = rng.End
		case ok:
			next[p] = hi + 1
		default:
			next[p] = rng.Start
		}
		more = more || next[p] < rng.End
	}
	return next, more
}
