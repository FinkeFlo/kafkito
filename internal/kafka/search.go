// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

	// StopOnLimit returns once the first Limit matches in the search
	// direction are known: Limit matches were found and every partition that
	// is not done has read past the last of them. A lagging partition can
	// keep a call waiting up to Timeout. Otherwise we scan until the page
	// cannot change any more (see pageFull) or the budget ends the call.
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
//
// Scanned, Matched and ParseErrors count only what lies behind NextCursors,
// i.e. what a follow-up call does not read again, so their sums across a
// chain of calls are exact totals.
type SearchStats struct {
	// Scanned counts the records this call read that the cursor moved past.
	Scanned int `json:"scanned"`
	// Read counts every record this call read, including the ones a
	// follow-up call reads again. Scanned <= Read <= budget.
	Read int `json:"read"`
	// Matched counts the returned matches: every match the cursor moved past.
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
	// partition (see searchChunk). A budget stop inside a chunk leaves that
	// chunk to the next call.
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

	pg := sc.page(ranges, opts.Limit)
	carryDoneCursors(pg.next, opts.Cursors, offs.parts)

	return &SearchResult{Messages: pg.messages, Stats: SearchStats{
		Scanned:           pg.scanned,
		Read:              sc.read,
		Matched:           pg.matched,
		BudgetExhausted:   budgetExhausted,
		TimedOut:          timedOut,
		MoreAvailable:     pg.more,
		Direction:         opts.Direction,
		NextCursors:       pg.next,
		ResolvedRange:     ranges,
		ParseErrors:       pg.parseErrors,
		ParseErrorOffsets: pg.parseErrorOffsets,
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
// opts.Timeout elapses. The budget holds per record: a call never reads
// more than opts.Budget records. The limit is checked after every poll, and
// StopOnLimit stops only once the page is settled (see settled).
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
		n := min(len(batch.records), opts.Budget-sc.read)
		for _, rec := range batch.records[:n] {
			sc.visit(ctx, rec)
		}
		sc.finish(cutBatch(batch, n))
		if sc.read >= opts.Budget {
			return true, false, nil
		}
		if opts.StopOnLimit && sc.confirmed >= opts.Limit && sc.settled(opts.Limit) {
			break
		}
		if sc.pageFull(opts.Limit) {
			// Reading on could not change the page: every partition is done
			// or holds a full page, and the cursors would move back anyway.
			break
		}
	}
	return false, false, nil
}

// cutBatch returns batch as far as its first n records were visited: a
// partition with a record past n neither finished its chunk nor its range
// in this poll.
func cutBatch(batch recordBatch, n int) recordBatch {
	if n >= len(batch.records) {
		return batch
	}
	open := make(map[int32]bool)
	for _, rec := range batch.records[n:] {
		open[rec.Partition] = true
	}
	notOpen := func(p int32) bool { return !open[p] }
	out := recordBatch{records: batch.records[:n]}
	for _, p := range batch.drained {
		if notOpen(p) {
			out.drained = append(out.drained, p)
		}
	}
	for _, p := range batch.ended {
		if notOpen(p) {
			out.ended = append(out.ended, p)
		}
	}
	for _, ch := range batch.chunks {
		if notOpen(ch.partition) {
			out.chunks = append(out.chunks, ch)
		}
	}
	return out
}

// searchScan accumulates the matches and stats of one SearchMessages call
// and tracks, per partition, how far the range was read without a gap.
type searchScan struct {
	match       matcher
	dec         recordDecoder
	newestFirst bool
	matches     []Message
	// read counts every visited record.
	read int
	// visited and errored hold, per partition, the offsets of the visited
	// records and of those that failed to parse. page counts the ones
	// behind the cursor.
	visited map[int32][]int64
	errored map[int32][]int64
	// errDetails keeps up to parseErrorOffsetsCap parse errors per
	// partition, in the order they were read.
	errDetails map[int32][]ParseErrorOffset
	// confirmed counts the matches at or above their partition's frontier,
	// i.e. the matches this call can return (before the limit).
	confirmed int
	// confirmedIn counts the confirmed matches per partition.
	confirmedIn map[int32]int
	ranges      map[int32]PartitionRange
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
	// highestTS is the timestamp of the record at highest (oldest-first).
	highestTS map[int32]int64
	// pendingLow is, per partition, the lowest visited record of the chunk
	// being read (newest-first); frontierTS is the timestamp of the lowest
	// visited record at or above the frontier, set once such a record exists.
	pendingLow map[int32]Message
	frontierTS map[int32]int64
	// ended marks the partitions whose range was read to its end.
	ended map[int32]bool
	// drained marks the partitions this call reads no further, because their
	// range ended or the reader gave up on it.
	drained map[int32]bool
}

func newSearchScan(mt matcher, dec recordDecoder, ranges map[int32]PartitionRange, dir SearchDirection) *searchScan {
	sc := &searchScan{
		match:       mt,
		dec:         dec,
		newestFirst: dir == DirNewestFirst,
		visited:     make(map[int32][]int64, len(ranges)),
		errored:     make(map[int32][]int64),
		errDetails:  make(map[int32][]ParseErrorOffset),
		pending:     make(map[int32]int, len(ranges)),
		confirmedIn: make(map[int32]int, len(ranges)),
		ranges:      ranges,
		frontier:    make(map[int32]int64, len(ranges)),
		stuck:       make(map[int32]bool),
		highest:     make(map[int32]int64, len(ranges)),
		highestTS:   make(map[int32]int64, len(ranges)),
		pendingLow:  make(map[int32]Message, len(ranges)),
		frontierTS:  make(map[int32]int64, len(ranges)),
		ended:       make(map[int32]bool, len(ranges)),
		drained:     make(map[int32]bool, len(ranges)),
	}
	for p, rng := range ranges {
		sc.frontier[p] = rng.End
	}
	return sc
}

func (sc *searchScan) visit(ctx context.Context, rec *kgo.Record) {
	p := rec.Partition
	ts := rec.Timestamp.UnixMilli()
	if cur, ok := sc.highest[p]; !ok || rec.Offset > cur {
		sc.highest[p] = rec.Offset
		sc.highestTS[p] = ts
	}
	if low, ok := sc.pendingLow[p]; sc.newestFirst && (!ok || rec.Offset < low.Offset) {
		sc.pendingLow[p] = Message{Offset: rec.Offset, Timestamp: ts}
	}
	sc.read++
	sc.visited[p] = append(sc.visited[p], rec.Offset)
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
			sc.confirmedIn[p]++
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
	for _, p := range batch.drained {
		sc.drained[p] = true
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
		if low, ok := sc.pendingLow[p]; ok {
			sc.frontierTS[p] = low.Timestamp
			delete(sc.pendingLow, p)
		}
		sc.confirmed += sc.pending[p]
		sc.confirmedIn[p] += sc.pending[p]
		sc.pending[p] = 0
	}
}

// confirmedMatches returns the matches this call can report. Newest-first,
// it drops the matches below their partition's frontier: they come from a
// chunk that was not read to its end, so the next call, which continues at
// the frontier, reads them again, and keeping them would also break the
// newest-first order across calls.
func (sc *searchScan) confirmedMatches() []Message {
	if !sc.newestFirst {
		return sc.matches
	}
	// A new slice: settled calls this while the scan still collects matches.
	out := make([]Message, 0, len(sc.matches))
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
	msg := err.Error()
	if sc.dec.masks {
		msg = parseErrorWithheld
	}
	slog.WarnContext(ctx, "search: skipping message – parse error",
		"partition", rec.Partition,
		"offset", rec.Offset,
		"error", msg)
	p := rec.Partition
	sc.errored[p] = append(sc.errored[p], rec.Offset)
	pe := ParseErrorOffset{Partition: p, Offset: rec.Offset, Error: msg}
	if len(sc.errDetails[p]) < parseErrorOffsetsCap {
		sc.errDetails[p] = append(sc.errDetails[p], pe)
		return
	}
	if sc.newestFirst {
		// The page reports the errors above the cursor, so newest-first
		// keeps the highest offsets; reading goes up within a chunk.
		ds := sc.errDetails[p]
		lowest := 0
		for i, d := range ds {
			if d.Offset < ds[lowest].Offset {
				lowest = i
			}
		}
		if ds[lowest].Offset < pe.Offset {
			ds[lowest] = pe
		}
	}
}

// pageFull reports whether reading on could not change this call's page:
// every partition is done or holds limit confirmed matches, and page keeps
// at most limit matches of a partition, the ones in front in offset order.
func (sc *searchScan) pageFull(limit int) bool {
	for p, rng := range sc.ranges {
		if sc.confirmedIn[p] >= limit {
			continue
		}
		if sc.newestFirst && (sc.stuck[p] || sc.frontier[p] <= rng.Start) {
			continue
		}
		if !sc.newestFirst && sc.ended[p] {
			continue
		}
		return false
	}
	return true
}

// settled reports whether the first limit confirmed matches are the first
// limit matches of the whole range in the search direction, so a
// stop-on-limit call can end. Like forwardPageSettled in consume, polls may
// return one partition's records long before another's: every partition
// this call still reads must have read past the page's last match, so that
// no unread record can rank inside the page.
//
// Timestamps are taken as non-decreasing within a partition. Oldest-first,
// a partition's next unread record then ranks at or after {timestamp of the
// highest record read, p, highest+1}. Newest-first, the next record the
// frontier admits ranks at or after {timestamp of the lowest record read
// above the frontier, p, frontier-1}; the matches of an unfinished chunk
// are not on the page yet, so only the frontier counts.
func (sc *searchScan) settled(limit int) bool {
	var last *Message
	for p, rng := range sc.ranges {
		if sc.drained[p] || sc.confirmedIn[p] >= limit {
			// Done, or its own first limit matches already rank at or
			// before the page's last one.
			continue
		}
		var next Message
		if sc.newestFirst {
			if sc.stuck[p] || sc.frontier[p] <= rng.Start {
				continue
			}
			ts, ok := sc.frontierTS[p]
			if !ok {
				return false
			}
			next = Message{Timestamp: ts, Partition: p, Offset: sc.frontier[p] - 1}
		} else {
			hi, ok := sc.highest[p]
			if !ok {
				return false
			}
			next = Message{Timestamp: sc.highestTS[p], Partition: p, Offset: hi + 1}
		}
		if last == nil {
			page := pageMerge(sc.confirmedMatches(), limit, sc.newestFirst)
			if len(page) < limit {
				return false
			}
			last = &page[len(page)-1]
		}
		if compareMessages(next, *last, sc.newestFirst) < 0 {
			return false
		}
	}
	return true
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

// searchPage is what one SearchMessages call returns: the matches and
// counts behind the cursors in next.
type searchPage struct {
	messages          []Message
	next              map[int32]int64
	more              bool
	scanned, matched  int
	parseErrors       int
	parseErrorOffsets []ParseErrorOffset
}

// page cuts the confirmed matches to limit and moves every cursor back to
// the first match that did not fit, so the next call returns it. It then
// reports only what lies behind the cursors: the matches, the records and
// the parse errors a follow-up call does not read again.
func (sc *searchScan) page(ranges map[int32]PartitionRange, limit int) searchPage {
	next, more := sc.continuation(ranges)
	matches := sc.confirmedMatches()
	for p, cut := range pageCut(matches, limit, sc.newestFirst) {
		more = true
		if sc.newestFirst {
			next[p] = max(next[p], cut+1)
		} else {
			next[p] = min(next[p], cut)
		}
	}
	behind := func(p int32, off int64) bool {
		c, ok := next[p]
		if !ok {
			return false
		}
		if sc.newestFirst {
			return off >= c
		}
		return off < c
	}
	pg := searchPage{messages: []Message{}, next: next, more: more}
	for _, m := range matches {
		if behind(m.Partition, m.Offset) {
			pg.messages = append(pg.messages, m)
		}
	}
	sortMessages(pg.messages, sc.newestFirst)
	pg.matched = len(pg.messages)
	parts := make([]int32, 0, len(sc.visited))
	for p, offs := range sc.visited {
		parts = append(parts, p)
		for _, off := range offs {
			if behind(p, off) {
				pg.scanned++
			}
		}
	}
	slices.Sort(parts)
	for _, p := range parts {
		for _, off := range sc.errored[p] {
			if behind(p, off) {
				pg.parseErrors++
			}
		}
		details := slices.Clone(sc.errDetails[p])
		slices.SortFunc(details, func(a, b ParseErrorOffset) int { return cmp.Compare(a.Offset, b.Offset) })
		for _, pe := range details {
			if behind(p, pe.Offset) && len(pg.parseErrorOffsets) < parseErrorOffsetsCap {
				pg.parseErrorOffsets = append(pg.parseErrorOffsets, pe)
			}
		}
	}
	return pg
}

// pageCut picks the first limit matches and returns, per partition, the
// offset of the first match that did not fit: the lowest oldest-first, the
// highest newest-first. It takes every partition's matches in offset order
// and merges them by time, so the matches a page keeps of a partition are
// always the ones in front of its cut, even when timestamps within the
// partition are out of order. With ordered timestamps these are the first
// limit matches by time among the matches this call confirmed.
func pageCut(matches []Message, limit int, newestFirst bool) map[int32]int64 {
	byPart, head := mergeByTime(matches, limit, newestFirst)
	cuts := make(map[int32]int64)
	for p, ms := range byPart {
		if head[p] < len(ms) {
			cuts[p] = ms[head[p]].Offset
		}
	}
	return cuts
}

// pageMerge returns the matches pageCut keeps, in page order.
func pageMerge(matches []Message, limit int, newestFirst bool) []Message {
	byPart, head := mergeByTime(matches, limit, newestFirst)
	page := make([]Message, 0, min(limit, len(matches)))
	for p, ms := range byPart {
		page = append(page, ms[:head[p]]...)
	}
	sortMessages(page, newestFirst)
	return page
}

// mergeByTime groups matches by partition in offset order (descending
// newest-first) and merges them by time. head[p] is how many of partition
// p's matches are among the first limit.
func mergeByTime(matches []Message, limit int, newestFirst bool) (byPart map[int32][]Message, head map[int32]int) {
	byPart = make(map[int32][]Message)
	for _, m := range matches {
		byPart[m.Partition] = append(byPart[m.Partition], m)
	}
	parts := make([]int32, 0, len(byPart))
	for p, ms := range byPart {
		parts = append(parts, p)
		slices.SortFunc(ms, func(a, b Message) int {
			if newestFirst {
				return cmp.Compare(b.Offset, a.Offset)
			}
			return cmp.Compare(a.Offset, b.Offset)
		})
	}
	slices.Sort(parts)
	head = make(map[int32]int, len(byPart))
	for range min(limit, len(matches)) {
		best := int32(-1)
		for _, p := range parts {
			if head[p] == len(byPart[p]) {
				continue
			}
			if best < 0 || compareMessages(byPart[p][head[p]], byPart[best][head[best]], newestFirst) < 0 {
				best = p
			}
		}
		head[best]++
	}
	return byPart, head
}
