// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// copyBatchSize is the number of records fetched from the source cluster in
// each iteration. Capped by the registry's own per-page limit.
const copyBatchSize = 500

// maxCopyBodyBytes limits the copy request body size.
const maxCopyBodyBytes = 32 * 1024

// copyProduceTimeout budgets one ProduceBatch call, i.e. a whole page of up to
// copyBatchSize records, not a single record. The overall job stays bounded by
// the 4 h ceiling on opCtx.
const copyProduceTimeout = 60 * time.Second

// copyValidateTimeout budgets the up-front destination metadata lookups. Kept
// well under the 30 s request-timeout middleware, since these run before the
// SSE stream opens and a failure there is still a plain JSON response.
const copyValidateTimeout = 5 * time.Second

// copyProgressInterval bounds how long the SSE stream may stay silent while
// the job is making progress that produces no events (pages whose records are
// all filtered out by the time window or skipped as unreproducible). Once the
// request deadline has fired, a write to the stream is the only way to notice
// the client is gone, so this also bounds how long a job keeps running after
// the browser went away — see the disconnect discussion in CopyMessages.
const copyProgressInterval = 2 * time.Second

// maxEmptyPageRetries / emptyPageRetryDelay guard against silently truncating a
// copy. An empty page is ambiguous: ConsumeMessages returns
// (NextCursor=nil, HasMore=false) both when the source range is genuinely
// drained and when a poll came back empty transiently — it gives up after two
// consecutive empty polls (~1s at FetchMaxWait(500ms)), which a leader election
// or a briefly stalled broker can produce while records remain. Taking that at
// face value would end the job and report success, so re-read the same cursor a
// few times first. A retry cannot duplicate anything: an empty page produced
// nothing, and the cursor is an offset seek.
const (
	maxEmptyPageRetries = 2
	emptyPageRetryDelay = time.Second
)

// maxConcurrentCopies caps how many copy jobs may run at once across the whole
// process. Each job holds a request goroutine, a destination producer client
// and a fresh consumer client per page for up to 4 hours, so an unbounded
// number of them is a trivial way to exhaust file descriptors and broker
// connections. Four is enough for the realistic "an operator plus a couple of
// scripted migrations" case while keeping the worst case at a handful of
// clients per cluster; excess callers get a 429 and can retry.
const maxConcurrentCopies = 4

// copySlots is the concurrency semaphore for copy jobs. Package-level (rather
// than a field on apiServer) so it caps the process, not a handler instance,
// and so tests can fill or replace it.
var copySlots = make(chan struct{}, maxConcurrentCopies)

// tryAcquireCopySlot takes a copy slot without blocking, reporting whether it
// got one. Callers that succeed must releaseCopySlot exactly once.
func tryAcquireCopySlot() bool {
	select {
	case copySlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseCopySlot() {
	select {
	case <-copySlots:
	default:
	}
}

// copyRegistry is the part of *kafkapkg.Registry the copy job uses. Tests
// substitute a fake to drive the SSE stream without a broker.
type copyRegistry interface {
	ConfigFor(name string) (config.ClusterConfig, bool)
	UseAdhoc(cfg config.ClusterConfig) (string, error)
	DescribeTopic(ctx context.Context, cluster, topic string) (*kafkapkg.TopicDetail, error)
	ConsumeMessages(ctx context.Context, cluster, topic string, opts kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error)
	ProduceBatch(ctx context.Context, cluster, topic string, reqs []kafkapkg.ProduceRequest) (int, error)
}

// copyJob is a validated copy request.
type copyJob struct {
	srcCluster, srcTopic   string
	destCluster, destTopic string
	// adhocDest is set when the destination came from dest_cluster_config.
	adhocDest bool

	// partition selects a single source partition; nil = all partitions.
	partition *int32
	// fromTSMs / toTSMs bound the source record timestamps: fromTSMs is
	// inclusive, toTSMs exclusive (matching internal/kafka's timeline
	// convention). Zero means "no bound"; for toTSMs the job substitutes its
	// start time, see CopyMessages.
	fromTSMs, toTSMs int64
	// limit caps the number of copied records; zero means no limit.
	limit int64
	// preservePartition routes each record to its source partition number.
	preservePartition bool
	// user is the RBAC subject recorded in the X-Kafkito-User header.
	user string
}

// copyProgressEvent is the SSE payload emitted while copying.
type copyProgressEvent struct {
	Copied int64 `json:"copied"`
	// Skipped counts source records that could not be reproduced verbatim
	// (see copyProduceRequest) and were left out of the destination topic.
	Skipped int64  `json:"skipped,omitempty"`
	Done    bool   `json:"done,omitempty"`
	Error   string `json:"error,omitempty"`
}

// copyStream is the SSE response body: the read side of the pipe the copy
// goroutine writes events into. The generated response writer closes it when
// it stops reading (stream finished or a write to the client failed), which
// stops the job.
type copyStream struct {
	*io.PipeReader
	stop context.CancelFunc
}

func (s copyStream) Close() error {
	s.stop()
	return s.PipeReader.Close()
}

// copyStreamResponse streams the copy progress as text/event-stream. The
// generated 200 response writes Content-Type, reads the body in chunks and
// flushes after each one; this wrapper adds the caching headers.
type copyStreamResponse struct {
	body copyStream
}

func (c copyStreamResponse) VisitCopyMessagesResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// The only error is a failed write to a client that went away. The
	// status is already sent, the job is stopped by the body's Close, and
	// reporting it would log a spurious 500.
	_ = gen.CopyMessages200TexteventStreamResponse{Body: c.body}.VisitCopyMessagesResponse(w)
	return nil
}

// CopyMessages reads records from the source topic and reproduces them on the
// destination topic, streaming SSE progress events to the caller.
//
// The source cluster ({cluster} in the URL) is already resolved by the
// upstream middleware, and the RBAC middleware checked topic:consume on it.
//
// The copy is a snapshot: when the caller sets no to_ts_ms, the job's own start
// time becomes the exclusive upper bound, so records produced to the source
// *after* the copy started are not included. Without that bound a copy of a
// live topic would never finish, because every page re-reads the source's
// high-watermark and a topic that is written to faster than it is copied keeps
// extending the work until the 4 h safety ceiling. (The bound is compared
// against broker-assigned record timestamps, so a source cluster whose clock
// runs ahead of kafkito's can leave its newest records out.)
//
// Records the destination cannot receive verbatim are counted as skipped, never
// silently altered; see copyProduceRequest.
//
// Streaming: everything that can fail is checked before the stream opens, so
// it is still a JSON error response. Then a goroutine runs the job and writes
// events into an io.Pipe whose read side is the response body. The job stops
// when the client goes away — the request context is cancelled, or a write to
// the client fails and the response writer closes the body — and releases its
// concurrency slot when the goroutine ends.
func (s *apiServer) CopyMessages(ctx context.Context, req gen.CopyMessagesRequestObject) (gen.CopyMessagesResponseObject, error) {
	r := httpRequestFromContext(ctx)
	job, err := s.copyJobFor(req, r)
	if err != nil {
		return nil, err
	}

	// Prod-cluster check for the DESTINATION.
	if err := prodConfirmationError(s.copyReg, job.destCluster, r); err != nil {
		return nil, err
	}

	// RBAC for the DESTINATION: resolvePermission/rbacMiddleware only checks
	// the source ({cluster}/{topic} from the URL) as "topic:consume"; the
	// destination is an arbitrary cluster/topic named in the body, so it
	// needs its own explicit "topic:produce" check here, mirroring what
	// produceMessage gets from the middleware for its own {cluster}/{topic}.
	// Ad-hoc/private destinations bypass RBAC entirely, same as elsewhere:
	// the caller supplies their own credentials and the broker enforces its
	// own ACLs.
	if !job.adhocDest && s.policy.Enabled() {
		if !s.policy.Allow(job.user, job.destCluster, "topic", job.destTopic, "produce") {
			resource, action := "topic:"+job.destTopic, "produce"
			return gen.CopyMessages403JSONResponse{ForbiddenJSONResponse: gen.ForbiddenJSONResponse{
				Error: "forbidden", Resource: &resource, Action: &action,
			}}, nil
		}
	}

	// Take a concurrency slot before doing any broker work or opening the
	// stream: shedding load is cheaper than starting a job we then have to
	// abandon, and a 429 is a far clearer signal than a stalled stream.
	if !tryAcquireCopySlot() {
		code, retryAfter := "copy_concurrency_limit", 30
		return gen.CopyMessages429JSONResponse{
			Body: gen.Error{
				Error: fmt.Sprintf("too many concurrent copy jobs (limit %d): retry shortly", maxConcurrentCopies),
				Code:  &code,
			},
			Headers: gen.CopyMessages429ResponseHeaders{RetryAfter: &retryAfter},
		}, nil
	}
	started := false
	defer func() {
		if !started {
			releaseCopySlot()
		}
	}()

	// Everything that can be checked up front must be checked up front: once
	// the SSE headers are out the status is 200 and a problem can only be
	// reported as an error event, which the UI shows after the user already
	// believes the copy started.
	if err := s.validateCopyDestination(ctx, job); err != nil {
		return nil, err
	}

	// Decouple the job from the 30 s request-timeout middleware
	// (middleware.Timeout in server.go), which would otherwise kill a copy
	// after half a minute. context.WithoutCancel strips the parent's deadline
	// AND its cancellation while keeping all key-value pairs (auth principal,
	// private cluster config, …). A client disconnect is wired back in
	// explicitly below; the request deadline is deliberately not.
	jobCtx, jobCancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Hour)
	// net/http cancels the request context when the client goes away (and
	// when the handler returns). Once the 30 s deadline has fired the context
	// is done for good and a later disconnect only shows up as a failed
	// write, which the response writer turns into a Close of the body.
	stopOnDisconnect := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			jobCancel()
		}
	})

	pr, pw := io.Pipe()
	started = true
	go func() {
		defer releaseCopySlot()
		defer stopOnDisconnect()
		defer jobCancel()
		defer pw.Close() //nolint:errcheck // closing the write side of a pipe cannot fail
		s.runCopy(jobCtx, pw, job)
	}()
	return copyStreamResponse{body: copyStream{PipeReader: pr, stop: jobCancel}}, nil
}

// copyJobFor validates the request body and resolves the destination cluster.
func (s *apiServer) copyJobFor(req gen.CopyMessagesRequestObject, r *http.Request) (copyJob, error) {
	body := req.Body
	job := copyJob{
		srcCluster:        req.Cluster,
		srcTopic:          req.Topic,
		destTopic:         strings.TrimSpace(body.DestTopic),
		partition:         body.Partition,
		fromTSMs:          deref(body.FromTsMs),
		toTSMs:            deref(body.ToTsMs),
		limit:             deref(body.Limit),
		preservePartition: deref(body.PreservePartition),
		user:              rbacSubject(r, s.policy),
	}
	if job.destTopic == "" {
		return job, badRequest("dest_topic is required")
	}
	destCluster := deref(body.DestCluster)
	if destCluster == "" && body.DestClusterConfig == nil {
		return job, badRequest("dest_cluster or dest_cluster_config is required")
	}
	if destCluster != "" && body.DestClusterConfig != nil {
		return job, badRequest("dest_cluster and dest_cluster_config are mutually exclusive")
	}

	// Resolve the destination cluster name. For named clusters and for
	// ad-hoc configs this is the same deterministic internal registry name
	// used for the source (resolvePrivateClusterParam rewrites the source's
	// {cluster} URL param the same way), so comparing the two below
	// correctly detects "same actual cluster" even across differently
	// labelled private-cluster configs that point at the same broker.
	if cfg := body.DestClusterConfig; cfg != nil {
		if err := validatePrivateClusterConfig(*cfg); err != nil {
			return job, badRequest(fmt.Errorf("dest_cluster_config: %w", err).Error())
		}
		name, err := s.copyReg.UseAdhoc(*cfg)
		if err != nil {
			return job, badRequest(fmt.Errorf("dest_cluster_config: %w", err).Error())
		}
		job.destCluster, job.adhocDest = name, true
	} else {
		job.destCluster = strings.TrimSpace(destCluster)
	}

	// Refuse to copy a topic into itself: even with the snapshot upper bound
	// this reads its own writes within the first page, and the intent is
	// almost certainly a mistake.
	if job.destCluster == job.srcCluster && job.destTopic == job.srcTopic {
		return job, badRequest("dest_cluster/dest_topic must differ from the source")
	}
	return job, nil
}

// runCopy runs the copy loop, writing progress events to w. It returns when
// the copy is complete, failed (reported as a final error event), ctx is done
// or a write to w fails.
func (s *apiServer) runCopy(ctx context.Context, w io.Writer, job copyJob) {
	lastEvent := time.Now()
	sendEvent := func(ev copyProgressEvent) bool {
		lastEvent = time.Now()
		b, _ := json.Marshal(ev)
		_, werr := fmt.Fprintf(w, "data: %s\n\n", b)
		return werr == nil
	}

	var (
		copied       int64
		skipped      int64
		emptyPages   int
		cursor       *string
		from         kafkapkg.ConsumeFrom
		partitionOpt int32 = -1
		// donePartitions tracks source partitions whose records have started
		// exceeding toTSMs. Kafka guarantees non-decreasing timestamps
		// within a single partition, so once a partition crosses the bound
		// none of its later records can be back in range — but pages
		// interleave multiple partitions, so encountering one out-of-range
		// record must not abort partitions that are still in range.
		donePartitions = map[int32]bool{}
	)

	// The effective upper bound is always set (see CopyMessages), so page
	// one is offset-clamped by resolveTimestampOffsets inside ConsumeMessages
	// and later (cursor-driven) pages are clamped by the filter below.
	toTSMs := job.toTSMs
	if toTSMs == 0 {
		toTSMs = time.Now().UnixMilli()
	}

	if job.partition != nil {
		partitionOpt = *job.partition
	}

	if job.fromTSMs > 0 {
		from = kafkapkg.FromTimestamp
	} else {
		from = kafkapkg.FromStart
	}

	// Tell the client the stream is live before the first (potentially
	// multi-second) consume call, so the UI can switch to "copying" and a
	// client that is already gone is detected before any broker work.
	if !sendEvent(copyProgressEvent{}) {
		return
	}

	for job.limit <= 0 || copied < job.limit {

		batchLimit := copyBatchSize
		if job.limit > 0 {
			remaining := job.limit - copied
			if remaining < int64(batchLimit) {
				batchLimit = int(remaining)
			}
		}

		opts := kafkapkg.ConsumeOptions{
			Partition: partitionOpt,
			Limit:     batchLimit,
			From:      from,
			FromTSMs:  job.fromTSMs,
			ToTSMs:    toTSMs,
			Timeout:   15 * time.Second,
		}

		if cursor != nil {
			c, decErr := kafkapkg.DecodeCursor(*cursor)
			if decErr != nil {
				sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true, Error: "cursor decode: " + decErr.Error()})
				return
			}
			opts.From = kafkapkg.FromOffset
			opts.PartitionOffsets = c.Partitions
		}

		page, consumeErr := s.copyReg.ConsumeMessages(ctx, job.srcCluster, job.srcTopic, opts)
		if consumeErr != nil {
			if errors.Is(consumeErr, context.Canceled) || errors.Is(consumeErr, context.DeadlineExceeded) {
				// Client gone or safety ceiling hit.
				return
			}
			sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true, Error: "consume: " + consumeErr.Error()})
			return
		}

		// Distinguish "drained" from "the broker just gave us nothing" before
		// concluding the copy is complete — see maxEmptyPageRetries.
		if len(page.Messages) == 0 {
			if emptyPages >= maxEmptyPageRetries {
				break
			}
			emptyPages++
			if !sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped}) {
				return // client disconnected
			}
			select {
			case <-time.After(emptyPageRetryDelay):
			case <-ctx.Done():
				return
			}
			continue
		}
		emptyPages = 0

		// Build the whole page's produce batch first: one ProduceSync for the
		// page instead of one per record is the difference between a copy that
		// runs at broker speed and one that pays a round-trip per message.
		// franz-go preserves per-partition ordering within the batch.
		batch := make([]kafkapkg.ProduceRequest, 0, len(page.Messages))
		for _, msg := range page.Messages {
			// Filtered and skipped records emit nothing of their own, so
			// heartbeat per record: a page (or a whole run of pages) whose
			// records are all out of range or unreproducible must not leave the
			// stream — and therefore the disconnect check — silent.
			if time.Since(lastEvent) >= copyProgressInterval {
				if !sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped}) {
					return // client disconnected
				}
			}

			// A record at or past the requested end timestamp means this
			// partition is done; other partitions in the same page may
			// still have in-range records, so only that partition is
			// excluded going forward — the whole copy must not stop here.
			// The bound is exclusive, matching how ConsumeMessages clamps
			// page one by offset.
			if msg.Timestamp >= toTSMs {
				donePartitions[msg.Partition] = true
				continue
			}

			produceReq, ok := copyProduceRequest(msg, job.preservePartition, job.user)
			if !ok {
				skipped++
				continue
			}
			batch = append(batch, produceReq)
		}
		// batchLimit already shrank the fetch to the remaining allowance, and
		// each message yields at most one record, so the batch cannot overshoot
		// job.limit.

		if len(batch) > 0 {
			produceCtx, produceCancel := context.WithTimeout(ctx, copyProduceTimeout)
			produced, produceErr := s.copyReg.ProduceBatch(produceCtx, job.destCluster, job.destTopic, batch)
			produceCancel()

			// produced counts broker-acked records, which is exactly what
			// "copied" promises — report it even on error, since a partially
			// acknowledged page is the normal failure shape.
			copied += int64(produced)

			if produceErr != nil {
				if errors.Is(produceErr, context.Canceled) || errors.Is(produceErr, context.DeadlineExceeded) {
					return
				}
				sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true, Error: "produce: " + produceErr.Error()})
				return
			}
		}

		// One progress event per fetched page, so the client always hears back
		// within a page even when nothing was copied from it.
		if !sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped}) {
			return // client disconnected
		}

		if !page.HasMore || page.NextCursor == nil {
			break
		}

		// Drop partitions that have already crossed toTSMs so the next page
		// doesn't keep re-fetching (and re-checking) them; once every
		// partition in the cursor is done, the copy is complete.
		for p := range page.NextCursor.Partitions {
			if donePartitions[p] {
				delete(page.NextCursor.Partitions, p)
			}
		}
		if len(page.NextCursor.Partitions) == 0 {
			break
		}

		// Advance the cursor for the next iteration.
		encoded, encErr := kafkapkg.EncodeCursor(*page.NextCursor)
		if encErr != nil {
			sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true, Error: "cursor encode: " + encErr.Error()})
			return
		}
		cursor = &encoded
		// Subsequent pages use cursor-based offset seek, not timestamp.
		from = kafkapkg.FromOffset
	}

	sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true})
}

// validateCopyDestination checks the destination topic before the SSE stream
// opens, returning a 400 when the copy cannot work.
//
// Both checks used to fail mid-stream: a missing destination topic surfaced as
// a produce error after the first page, and preserve_partition against a
// narrower destination aborted somewhere in the middle, leaving a partial copy
// behind.
//
// A metadata lookup that fails for any other reason (broker unreachable, no
// Describe ACL, timeout) is deliberately NOT fatal here: it is indistinguishable
// from a transient blip, and the copy loop reports real broker failures as
// error events anyway. Only a positive "this topic does not exist" is a 400.
func (s *apiServer) validateCopyDestination(ctx context.Context, job copyJob) error {
	ctx, cancel := context.WithTimeout(ctx, copyValidateTimeout)
	defer cancel()

	destDetail, err := s.copyReg.DescribeTopic(ctx, job.destCluster, job.destTopic)
	switch {
	case errors.Is(err, kafkapkg.ErrUnknownCluster):
		return badRequest("unknown dest_cluster: " + job.destCluster)
	case err != nil && isTopicMissingErr(err):
		return badRequest(fmt.Sprintf("dest_topic %q does not exist on cluster %q: create it first (kafkito does not auto-create the destination)", job.destTopic, job.destCluster))
	case err != nil:
		s.log.WarnContext(ctx, "copy: destination pre-flight check skipped",
			"cluster", job.destCluster, "topic", job.destTopic, "err", err)
		return nil
	}

	if !job.preservePartition {
		return nil
	}

	// preserve_partition writes each record to its source partition index, so
	// the destination must be at least as wide as the widest source partition
	// being copied.
	var required int32
	if job.partition != nil && *job.partition >= 0 {
		required = *job.partition
	} else {
		srcDetail, srcErr := s.copyReg.DescribeTopic(ctx, job.srcCluster, job.srcTopic)
		if srcErr != nil {
			s.log.WarnContext(ctx, "copy: preserve_partition pre-flight check skipped",
				"cluster", job.srcCluster, "topic", job.srcTopic, "err", srcErr)
			return nil
		}
		required = highestPartition(srcDetail.Partitions)
	}
	if err := checkDestPartitions(job.destTopic, len(destDetail.Partitions), required); err != nil {
		return badRequest(err.Error())
	}
	return nil
}

// highestPartition returns the largest partition index in parts, or -1 when
// there are none.
func highestPartition(parts []kafkapkg.PartitionInfo) int32 {
	highest := int32(-1)
	for _, p := range parts {
		if p.Partition > highest {
			highest = p.Partition
		}
	}
	return highest
}

// checkDestPartitions reports whether a destination topic with destCount
// partitions can hold records routed to partition indexes up to and including
// highestSrcPartition (as preserve_partition does). Returns nil when it can.
func checkDestPartitions(destTopic string, destCount int, highestSrcPartition int32) error {
	if highestSrcPartition < 0 {
		return nil
	}
	if int64(destCount) > int64(highestSrcPartition) {
		return nil
	}
	return fmt.Errorf(
		"preserve_partition: destination topic %q has %d partition(s) but the source needs at least %d; "+
			"add partitions to the destination or drop preserve_partition",
		destTopic, destCount, highestSrcPartition+1,
	)
}

// isTopicMissingErr reports whether err says a topic does not exist, as opposed
// to a broker/transport failure. Matches both the registry's own wording and
// the broker error code it wraps.
func isTopicMissingErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "topic not found") ||
		strings.Contains(msg, "not found on cluster") ||
		strings.Contains(msg, "UNKNOWN_TOPIC_OR_PARTITION")
}

// copyProduceRequest turns a consumed source record into the produce request
// that reproduces it on the destination, reporting false when the record must
// be skipped instead.
//
// Records are skipped, never approximated, in three cases:
//
//   - Masked: the source cluster's data_masking policy redacted the value, so
//     what we hold is a redaction, not the record. Copying it would write the
//     redacted rendering into the destination as if it were real data — silent
//     data corruption that no later step can detect — and un-redacting is by
//     construction impossible. (Masking is a per-cluster policy, so a
//     destination that is allowed to see the raw data still cannot get it
//     through this route.)
//   - Schema-Registry-decoded key/value: see produceEncodingFor.
//   - Truncated value: ConsumeMessages caps Value at 64 KB (maxMessageValueBytes
//     in internal/kafka/consumer.go) to bound per-record memory during the batch
//     consume/produce loop this function feeds. What we hold for a larger
//     record is only the first 64 KB, so copying it would silently write a
//     truncated record instead of the original — the same "approximated
//     instead of skipped" corruption the other two cases guard against.
//     Unlike the single-message Replay dialog, a bulk copy has no per-record
//     UI to ask the user whether to proceed anyway, and re-fetching the full
//     value per truncated record here would reintroduce the unbounded
//     memory/latency cost the 64 KB cap exists to prevent at this scale — so
//     it is always skipped, reported like any other skip via `skipped`.
//
// Headers go into a fresh map: injectKafkitoProduceHeaders writes into
// ProduceRequest.Headers, and handing it msg.Headers would mutate the consumed
// record in place. HeadersB64 carries header values that are not valid UTF-8;
// without it they would arrive at the destination as the literal display text
// "0x<hex>" that recordToMessage puts in Headers.
func copyProduceRequest(msg kafkapkg.Message, preservePartition bool, user string) (kafkapkg.ProduceRequest, bool) {
	if msg.Masked || msg.ValueTruncated {
		return kafkapkg.ProduceRequest{}, false
	}

	key, keyEncoding, keyOK := produceEncodingFor(msg.Key, msg.KeyB64, msg.KeyEncoding)
	value, valueEncoding, valueOK := produceEncodingFor(msg.Value, msg.ValueB64, msg.ValueEncoding)
	if !keyOK || !valueOK {
		return kafkapkg.ProduceRequest{}, false
	}

	var destPartition *int32
	if preservePartition {
		p := msg.Partition
		destPartition = &p
	}

	headers := make(map[string]string, len(msg.Headers)+2)
	for k, v := range msg.Headers {
		headers[k] = v
	}

	out := kafkapkg.ProduceRequest{
		Partition:     destPartition,
		Key:           key,
		Value:         value,
		KeyEncoding:   keyEncoding,
		ValueEncoding: valueEncoding,
		Headers:       headers,
		HeadersB64:    msg.HeadersB64,
	}
	injectKafkitoProduceHeaders(&out, user)
	return out, true
}

// produceEncodingFor maps a consumed field's (rendered, base64, encoding)
// triple — as returned by the consumer for Message.Key/Value — to the
// (value, encoding) pair kafkapkg.ProduceRequest expects, so the record's
// key and value bytes are reproduced exactly on the destination. The record as
// a whole is not byte-for-byte identical: copyProduceRequest adds the two
// X-Kafkito-* provenance headers, overwriting any source header of the same
// name.
//
// "binary" payloads only retain their original bytes in the base64 form, so
// those use ProduceRequest's "base64" encoding. "empty" needs ProduceRequest's
// "empty" encoding, because "text" collapses an empty string to nil: a
// zero-length value re-produced as nil becomes a tombstone (deleting the key on
// a compacted destination) and a zero-length key re-produced as nil changes
// partitioning, since franz-go hashes an empty key but round-robins a nil one.
// "null" stays on "text" precisely because that collapse is the faithful
// reproduction there: nil in, nil out. "text", "json" and "xml" round-trip through the
// rendered string as-is via "text".
//
// Schema-Registry-decoded fields ("avro", "json_schema", "protobuf") are
// reported as not-ok: applySRDecoder overwrites the raw bytes with the decoded
// JSON rendering and discards the base64 form, so the original wire-format
// bytes are not recoverable from a Message at all.
func produceEncodingFor(rendered, b64, encoding string) (value, produceEncoding string, ok bool) {
	switch encoding {
	case "avro", "json_schema", "protobuf":
		return "", "", false
	case "binary":
		return b64, "base64", true
	case "empty":
		return "", "empty", true
	default: // "null", "json", "xml", "text"
		return rendered, "text", true
	}
}
