// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
	"github.com/go-chi/chi/v5"
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
// all filtered out by the time window or skipped as unreproducible). A write
// to the stream is the only way to notice the client is gone, so this also
// bounds how long a job keeps running after the browser went away — see the
// disconnect discussion in copyMessages.
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
// than a field on clusterAPI) so it caps the process, not a handler instance,
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

// copyRequest is the JSON body for POST /topics/{topic}/copy.
type copyRequest struct {
	// DestCluster names a server-configured cluster. Mutually exclusive with
	// DestClusterConfig.
	DestCluster string `json:"dest_cluster"`

	// DestClusterConfig allows the caller to pass an ad-hoc (private) cluster
	// configuration inline. Used when the destination is a browser-side private
	// cluster whose details are not known to the server. Mutually exclusive
	// with DestCluster.
	DestClusterConfig *config.ClusterConfig `json:"dest_cluster_config,omitempty"`

	DestTopic string `json:"dest_topic"`

	// Partition selects a single source partition. nil / absent = all partitions.
	Partition *int32 `json:"partition,omitempty"`

	// FromTSMs / ToTSMs are UNIX millisecond timestamps bounding which source
	// messages to copy. FromTSMs is inclusive, ToTSMs exclusive (matching
	// pkg/kafka's timeline convention). Zero means "no bound"; for ToTSMs the
	// handler substitutes the job's start time, see copyMessages.
	FromTSMs int64 `json:"from_ts_ms,omitempty"`
	ToTSMs   int64 `json:"to_ts_ms,omitempty"`

	// Limit caps the total number of messages to copy. Zero means no limit.
	Limit int64 `json:"limit,omitempty"`

	// PreservePartition routes each record to the same partition on the
	// destination topic as it came from on the source.
	PreservePartition bool `json:"preserve_partition,omitempty"`
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

// copyMessages reads records from the source topic and reproduces them on the
// destination topic, streaming SSE progress events to the caller.
//
// Route: POST /clusters/{cluster}/topics/{topic}/copy
// The source cluster is already resolved by the upstream middleware.
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
func (a *clusterAPI) copyMessages(w http.ResponseWriter, r *http.Request) {
	srcCluster := chi.URLParam(r, "cluster")
	srcTopic := chi.URLParam(r, "topic")

	// Parse request body.
	var req copyRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCopyBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}

	// Validate destination.
	req.DestTopic = strings.TrimSpace(req.DestTopic)
	if req.DestTopic == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dest_topic is required"})
		return
	}
	if req.DestCluster == "" && req.DestClusterConfig == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dest_cluster or dest_cluster_config is required"})
		return
	}
	if req.DestCluster != "" && req.DestClusterConfig != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dest_cluster and dest_cluster_config are mutually exclusive"})
		return
	}

	// Resolve destination cluster name. For named clusters and for ad-hoc
	// configs this is the same deterministic internal registry name used
	// for the source (resolvePrivateClusterParam rewrites the source's
	// {cluster} URL param the same way), so comparing the two below
	// correctly detects "same actual cluster" even across differently
	// labelled private-cluster configs that point at the same broker.
	destCluster, err := a.resolveDestCluster(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Refuse to copy a topic into itself: even with the snapshot upper bound
	// this reads its own writes within the first page, and the intent is
	// almost certainly a mistake.
	if destCluster == srcCluster && req.DestTopic == srcTopic {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dest_cluster/dest_topic must differ from the source"})
		return
	}

	// Prod-cluster check for the DESTINATION.
	if !a.requireProdConfirmation(w, r, destCluster) {
		return
	}

	// RBAC for the DESTINATION: resolvePermission/rbacMiddleware only checks
	// the source ({cluster}/{topic} from the URL) as "topic:consume"; the
	// destination is an arbitrary cluster/topic named in the body, so it
	// needs its own explicit "topic:produce" check here, mirroring what
	// produceMessage gets from the middleware for its own {cluster}/{topic}.
	// Ad-hoc/private destinations bypass RBAC entirely, same as elsewhere:
	// the caller supplies their own credentials and the broker enforces its
	// own ACLs.
	if req.DestClusterConfig == nil && a.policy.Enabled() {
		user := rbacSubject(r, a.policy)
		if !a.policy.Allow(user, destCluster, "topic", req.DestTopic, "produce") {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":    "forbidden",
				"resource": "topic:" + req.DestTopic,
				"action":   "produce",
			})
			return
		}
	}

	// Take a concurrency slot before doing any broker work or opening the
	// stream: shedding load is cheaper than starting a job we then have to
	// abandon, and a 429 is a far clearer signal than a stalled stream.
	if !tryAcquireCopySlot() {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": fmt.Sprintf("too many concurrent copy jobs (limit %d): retry shortly", maxConcurrentCopies),
			"code":  "copy_concurrency_limit",
		})
		return
	}
	defer releaseCopySlot()

	// Everything that can be checked up front must be checked up front: once
	// the SSE headers are out the status is 200 and a problem can only be
	// reported as an error event, which the UI shows after the user already
	// believes the copy started.
	if !a.validateCopyDestination(w, r, req, srcCluster, srcTopic, destCluster) {
		return
	}

	// Decouple the job from the 30 s request-timeout middleware
	// (middleware.Timeout in server.go), which would otherwise kill a copy
	// after half a minute. context.WithoutCancel strips the parent's deadline
	// AND its cancellation — including the cancellation the net/http server
	// performs when the client goes away — while keeping all key-value pairs
	// (auth principal, private cluster config, …). So the request context is
	// deliberately not a disconnect signal here.
	//
	// A failed write to the SSE stream is the only disconnect signal we get.
	// sendEvent reports it and every caller aborts the job, which means
	// disconnect is detected at a granularity of one fetched page or one
	// copyProgressInterval heartbeat, whichever comes first — not instantly.
	opCtx, opCancel := context.WithTimeout(
		context.WithoutCancel(r.Context()),
		4*time.Hour,
	)
	defer opCancel()

	// SSE headers — must be written before the first Flush.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	lastEvent := time.Now()
	sendEvent := func(ev copyProgressEvent) bool {
		lastEvent = time.Now()
		b, _ := json.Marshal(ev)
		_, werr := fmt.Fprintf(w, "data: %s\n\n", b)
		if canFlush {
			flusher.Flush()
		}
		return werr == nil
	}

	// user is stable for the whole request; resolve it once rather than on
	// every produced record.
	user := rbacSubject(r, a.policy)

	// Copy loop.
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

	// The effective upper bound is always set (see the doc comment), so page
	// one is offset-clamped by resolveTimestampOffsets inside ConsumeMessages
	// and later (cursor-driven) pages are clamped by the filter below.
	toTSMs := req.ToTSMs
	if toTSMs == 0 {
		toTSMs = time.Now().UnixMilli()
	}

	if req.Partition != nil {
		partitionOpt = *req.Partition
	}

	if req.FromTSMs > 0 {
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

	for {
		// Respect an optional per-copy limit.
		if req.Limit > 0 && copied >= req.Limit {
			break
		}

		batchLimit := copyBatchSize
		if req.Limit > 0 {
			remaining := req.Limit - copied
			if remaining < int64(batchLimit) {
				batchLimit = int(remaining)
			}
		}

		opts := kafkapkg.ConsumeOptions{
			Partition: partitionOpt,
			Limit:     batchLimit,
			From:      from,
			FromTSMs:  req.FromTSMs,
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

		page, consumeErr := a.reg.ConsumeMessages(opCtx, srcCluster, srcTopic, opts)
		if consumeErr != nil {
			if errors.Is(consumeErr, context.Canceled) || errors.Is(consumeErr, context.DeadlineExceeded) {
				// Safety ceiling hit (the client-disconnect path aborts via a
				// failed sendEvent instead).
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
			case <-opCtx.Done():
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

			produceReq, ok := copyProduceRequest(msg, req.PreservePartition, user)
			if !ok {
				skipped++
				continue
			}
			batch = append(batch, produceReq)
		}
		// batchLimit already shrank the fetch to the remaining allowance, and
		// each message yields at most one record, so the batch cannot overshoot
		// req.Limit.

		if len(batch) > 0 {
			produceCtx, produceCancel := context.WithTimeout(opCtx, copyProduceTimeout)
			produced, produceErr := a.reg.ProduceBatch(produceCtx, destCluster, req.DestTopic, batch)
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
// opens, writing a JSON error and reporting false when the copy cannot work.
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
func (a *clusterAPI) validateCopyDestination(
	w http.ResponseWriter,
	r *http.Request,
	req copyRequest,
	srcCluster, srcTopic, destCluster string,
) bool {
	ctx, cancel := context.WithTimeout(r.Context(), copyValidateTimeout)
	defer cancel()

	destDetail, err := a.reg.DescribeTopic(ctx, destCluster, req.DestTopic)
	switch {
	case errors.Is(err, kafkapkg.ErrUnknownCluster):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown dest_cluster: " + destCluster})
		return false
	case err != nil && isTopicMissingErr(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("dest_topic %q does not exist on cluster %q: create it first (kafkito does not auto-create the destination)", req.DestTopic, destCluster),
		})
		return false
	case err != nil:
		a.log.WarnContext(ctx, "copy: destination pre-flight check skipped",
			"cluster", destCluster, "topic", req.DestTopic, "err", err)
		return true
	}

	if !req.PreservePartition {
		return true
	}

	// preserve_partition writes each record to its source partition index, so
	// the destination must be at least as wide as the widest source partition
	// being copied.
	required := int32(-1)
	if req.Partition != nil && *req.Partition >= 0 {
		required = *req.Partition
	} else {
		srcDetail, srcErr := a.reg.DescribeTopic(ctx, srcCluster, srcTopic)
		if srcErr != nil {
			a.log.WarnContext(ctx, "copy: preserve_partition pre-flight check skipped",
				"cluster", srcCluster, "topic", srcTopic, "err", srcErr)
			return true
		}
		required = highestPartition(srcDetail.Partitions)
	}
	if err := checkDestPartitions(req.DestTopic, len(destDetail.Partitions), required); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return false
	}
	return true
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
// Records are skipped, never approximated, in two cases:
//
//   - Masked: the source cluster's data_masking policy redacted the value, so
//     what we hold is a redaction, not the record. Copying it would write the
//     redacted rendering into the destination as if it were real data — silent
//     data corruption that no later step can detect — and un-redacting is by
//     construction impossible. (Masking is a per-cluster policy, so a
//     destination that is allowed to see the raw data still cannot get it
//     through this route.)
//   - Schema-Registry-decoded key/value: see produceEncodingFor.
//
// Headers go into a fresh map: injectKafkitoProduceHeaders writes into
// ProduceRequest.Headers, and handing it msg.Headers would mutate the consumed
// record in place. HeadersB64 carries header values that are not valid UTF-8;
// without it they would arrive at the destination as the literal display text
// "0x<hex>" that recordToMessage puts in Headers.
func copyProduceRequest(msg kafkapkg.Message, preservePartition bool, user string) (kafkapkg.ProduceRequest, bool) {
	if msg.Masked {
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
// reproduction there: nil in, nil out. "text" and "json" round-trip through the
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
	default: // "null", "json", "text"
		return rendered, "text", true
	}
}

// resolveDestCluster returns the internal cluster name to use for producing to
// the destination. For named clusters the name is used directly (the registry
// will return ErrUnknownCluster if it isn't configured). For ad-hoc configs the
// cluster is registered on demand via UseAdhoc.
func (a *clusterAPI) resolveDestCluster(req copyRequest) (string, error) {
	if req.DestClusterConfig != nil {
		if err := validatePrivateClusterConfig(*req.DestClusterConfig); err != nil {
			return "", fmt.Errorf("dest_cluster_config: %w", err)
		}
		name, err := a.reg.UseAdhoc(*req.DestClusterConfig)
		if err != nil {
			return "", fmt.Errorf("dest_cluster_config: %w", err)
		}
		return name, nil
	}
	return strings.TrimSpace(req.DestCluster), nil
}
