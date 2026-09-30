// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// recordScan describes one read of a topic by a record reader
// (ConsumeMessages, SearchMessages, FetchRawMessageValue): which offsets to
// read per partition and in which order.
type recordScan struct {
	cluster, topic string
	role           string // client role in logs and errors, e.g. "consume"
	cfg            config.ClusterConfig
	// ranges holds the non-empty [Start, End) offset range per partition.
	ranges map[int32]PartitionRange
	// chunk > 0 walks every range backward in chunks of that many offsets,
	// so the newest records come first; 0 reads every range forward once.
	// A chunk the drainAfter fallback ends is a hole in the range, so a
	// backward walk stops that partition there instead of reading on below.
	chunk int64
	// drainAfter is the number of consecutive polls without any record after
	// which every partition counts as drained. This is only a fallback:
	// PollFetches does not return empty fetches for drained partitions, so a
	// range normally ends when its last offset is seen.
	drainAfter int
}

// scanCursor is the read position of one partition. The current chunk is
// [pos, upper); records below lower are never yielded.
type scanCursor struct {
	lower, upper, pos int64
	done              bool // the record at upper-1 (or beyond) was seen
	// forced is set once the drainAfter fallback ended a chunk whose last
	// offset was never seen; the range then was not read to its end.
	forced bool
}

// recordBatch is what one poll of a scan produced.
type recordBatch struct {
	// records are the in-range records, in the order the client returned them.
	records []*kgo.Record
	// drained lists the partitions whose range this poll finished.
	drained []int32
	// ended lists the drained partitions whose range was read to its end,
	// i.e. not given up by the drainAfter fallback.
	ended []int32
	// chunks lists the chunks this poll finished, at most one per
	// partition. Every record of a finished chunk's partition in this batch
	// belongs to that chunk.
	chunks []finishedChunk
}

// finishedChunk is a chunk [lower, upper) of a partition's range that a
// poll finished. Backward walks finish a partition's chunks top down.
type finishedChunk struct {
	partition int32
	lower     int64
	// forced is set when the drainAfter fallback ended the chunk before its
	// last offset was seen; the chunk then was not read to its end.
	forced bool
}

// scanRecords reads the ranges of s and yields one batch per poll that
// returned in-range records or finished a range. Batches are the unit
// callers budget on: ConsumeMessages merges and truncates whole polls,
// SearchMessages checks its scan budget and limit after each poll.
//
// The sequence ends once every range is drained or the caller stops. It
// yields ctx.Err() when ctx ends and a wrapped error when a fetch fails, and
// stops after any error. The short-lived consumer client is created on the
// first pull and always closed when the sequence ends.
func (r *Messages) scanRecords(ctx context.Context, s recordScan) iter.Seq2[recordBatch, error] {
	return func(yield func(recordBatch, error) bool) {
		cursors := s.cursors()
		cl, err := r.scanClient(s, cursors)
		if err != nil {
			yield(recordBatch{}, err)
			return
		}
		defer cl.Close()

		emptyPolls := 0
		for len(cursors) > 0 {
			fetches := cl.PollFetches(ctx)
			if err := ctx.Err(); err != nil {
				yield(recordBatch{}, err)
				return
			}
			if err := s.fetchError(fetches); err != nil {
				yield(recordBatch{}, err)
				return
			}
			records := inRange(fetches, cursors)
			emptyPolls++
			if !fetches.Empty() {
				emptyPolls = 0
			}
			if emptyPolls >= s.drainAfter {
				forceDone(cursors)
			}
			drained := s.exhausted(cursors)
			batch := recordBatch{records: records, drained: drained, ended: readToEnd(cursors, drained), chunks: finished(cursors)}
			if (len(batch.records) > 0 || len(batch.drained) > 0 || len(batch.chunks) > 0) && !yield(batch, nil) {
				return
			}
			s.advance(cl, cursors)
		}
	}
}

func (s recordScan) cursors() map[int32]*scanCursor {
	out := make(map[int32]*scanCursor, len(s.ranges))
	for p, rng := range s.ranges {
		c := &scanCursor{lower: rng.Start, upper: rng.End, pos: rng.Start}
		if s.chunk > 0 {
			c.pos = max(rng.Start, rng.End-s.chunk)
		}
		out[p] = c
	}
	return out
}

func (r *Messages) scanClient(s recordScan, cursors map[int32]*scanCursor) (*kgo.Client, error) {
	offsets := make(map[int32]kgo.Offset, len(cursors))
	for p, c := range cursors {
		offsets[p] = kgo.NewOffset().At(c.pos)
	}
	opts := append(clientOptsDial(s.cfg, r.log.With("cluster", config.ClusterLogName(s.cluster), "role", s.role), r.adhocDial),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{s.topic: offsets}),
		kgo.FetchMaxWait(500*time.Millisecond),
		// An offset that is out of range, e.g. below a log start moved by
		// retention or DeleteRecords, resumes at the log start. This was
		// franz-go's default before v1.22; its default now rewinds by time.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// Transaction markers take up offsets too. Without them a range whose
		// last offset is a marker would never be seen to end.
		kgo.KeepControlRecords(),
	)
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("create %s client for topic %q on cluster %q: %w", s.role, s.topic, config.PublicClusterName(s.cluster), err)
	}
	return cl, nil
}

// fetchError returns the first fetch error that is not a context error;
// those are reported through ctx.Err() instead.
func (s recordScan) fetchError(fetches kgo.Fetches) error {
	for _, e := range fetches.Errors() {
		if isContextErr(e.Err) {
			continue
		}
		return fmt.Errorf("fetch topic %q partition %d on cluster %q: %w", s.topic, e.Partition, config.PublicClusterName(s.cluster), e.Err)
	}
	return nil
}

// inRange returns the records inside their partition's current chunk and
// marks a chunk done once its last offset, or any later one, shows up.
// Control records (transaction markers) only count for the latter.
func inRange(fetches kgo.Fetches, cursors map[int32]*scanCursor) []*kgo.Record {
	var batch []*kgo.Record
	fetches.EachRecord(func(rec *kgo.Record) {
		c, ok := cursors[rec.Partition]
		if !ok || rec.Offset < c.lower {
			return
		}
		if rec.Offset >= c.upper-1 {
			c.done = true
		}
		if rec.Offset < c.upper && !rec.Attrs.IsControl() {
			batch = append(batch, rec)
		}
	})
	return batch
}

// exhausted returns the partitions whose done chunk is the last one of their
// range, or a forced chunk of a backward walk, i.e. the partitions the next
// advance drops.
func (s recordScan) exhausted(cursors map[int32]*scanCursor) []int32 {
	var out []int32
	for p, c := range cursors {
		if c.done && s.last(c) {
			out = append(out, p)
		}
	}
	return out
}

// last reports whether the current chunk of c is the last one to read.
func (s recordScan) last(c *scanCursor) bool {
	return c.pos <= c.lower || (s.chunk > 0 && c.forced)
}

// finished returns the chunk every done partition just finished.
func finished(cursors map[int32]*scanCursor) []finishedChunk {
	var out []finishedChunk
	for p, c := range cursors {
		if c.done {
			out = append(out, finishedChunk{partition: p, lower: c.pos, forced: c.forced})
		}
	}
	return out
}

// forceDone ends the current chunk of every partition, marking the ones
// whose last offset was not seen as forced.
func forceDone(cursors map[int32]*scanCursor) {
	for _, c := range cursors {
		if !c.done {
			c.done, c.forced = true, true
		}
	}
}

// readToEnd returns the drained partitions that the drainAfter fallback
// never cut short.
func readToEnd(cursors map[int32]*scanCursor, drained []int32) []int32 {
	var out []int32
	for _, p := range drained {
		if !cursors[p].forced {
			out = append(out, p)
		}
	}
	return out
}

// advance moves every done partition to its next chunk, or drops it when its
// range is exhausted. Forward scans have a single chunk per partition.
func (s recordScan) advance(cl *kgo.Client, cursors map[int32]*scanCursor) {
	var reseek []int32
	next := make(map[int32]kgo.Offset)
	for p, c := range cursors {
		if !c.done {
			continue
		}
		c.done = false
		if s.chunk > 0 {
			reseek = append(reseek, p)
		}
		if s.last(c) {
			delete(cursors, p)
			continue
		}
		c.upper = c.pos
		c.pos = max(c.lower, c.upper-s.chunk)
		next[p] = kgo.NewOffset().At(c.pos)
	}
	if len(reseek) > 0 {
		cl.RemoveConsumePartitions(map[string][]int32{s.topic: reseek})
	}
	if len(next) > 0 {
		cl.AddConsumePartitions(map[string]map[int32]kgo.Offset{s.topic: next})
	}
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// sortMessages orders msgs newest first (timestamp desc, partition asc,
// offset desc) or oldest first (timestamp, partition, offset ascending).
func sortMessages(msgs []Message, newestFirst bool) {
	slices.SortFunc(msgs, func(a, b Message) int { return compareMessages(a, b, newestFirst) })
}

func compareMessages(a, b Message, newestFirst bool) int {
	sign := 1
	if newestFirst {
		sign = -1
	}
	return cmp.Or(
		sign*cmp.Compare(a.Timestamp, b.Timestamp),
		cmp.Compare(a.Partition, b.Partition),
		sign*cmp.Compare(a.Offset, b.Offset),
	)
}
