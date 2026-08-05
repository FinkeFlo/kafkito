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
	// messages to copy. Zero means "no bound".
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
	// (see produceEncodingFor) and were left out of the destination topic.
	Skipped int64  `json:"skipped,omitempty"`
	Done    bool   `json:"done,omitempty"`
	Error   string `json:"error,omitempty"`
}

// copyMessages reads records from the source topic and reproduces them on the
// destination topic, streaming SSE progress events to the caller.
//
// Route: POST /clusters/{cluster}/topics/{topic}/copy
// The source cluster is already resolved by the upstream middleware.
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

	// Refuse to copy a topic into itself: with no upper time bound this
	// would never terminate, since each produced record extends the
	// source's own high-watermark for the next page of the same copy job.
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

	// Set up a context that is decoupled from the upstream 30 s timeout
	// middleware but will be cancelled when the client disconnects.
	// context.WithoutCancel strips the parent's deadline while keeping all
	// key-value pairs (auth principal, private cluster config, …).
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

	sendEvent := func(ev copyProgressEvent) bool {
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
		cursor       *string
		from         kafkapkg.ConsumeFrom
		partitionOpt int32 = -1
		// donePartitions tracks source partitions whose records have started
		// exceeding req.ToTSMs. Kafka guarantees non-decreasing timestamps
		// within a single partition, so once a partition crosses the bound
		// none of its later records can be back in range — but pages
		// interleave multiple partitions, so encountering one out-of-range
		// record must not abort partitions that are still in range.
		donePartitions map[int32]bool
	)
	if req.ToTSMs > 0 {
		donePartitions = make(map[int32]bool)
	}

	if req.Partition != nil {
		partitionOpt = *req.Partition
	}

	if req.FromTSMs > 0 {
		from = kafkapkg.FromTimestamp
	} else {
		from = kafkapkg.FromStart
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
			ToTSMs:    req.ToTSMs,
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
				// Client disconnected or safety timeout hit.
				return
			}
			sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true, Error: "consume: " + consumeErr.Error()})
			return
		}

		for _, msg := range page.Messages {
			// A record past the requested end timestamp means this
			// partition is done; other partitions in the same page may
			// still have in-range records, so only that partition is
			// excluded going forward — the whole copy must not stop here.
			if req.ToTSMs > 0 && msg.Timestamp > req.ToTSMs {
				donePartitions[msg.Partition] = true
				continue
			}

			destPartition := (*int32)(nil)
			if req.PreservePartition {
				p := msg.Partition
				destPartition = &p
			}

			key, keyEncoding, keyOK := produceEncodingFor(msg.Key, msg.KeyB64, msg.KeyEncoding)
			value, valueEncoding, valueOK := produceEncodingFor(msg.Value, msg.ValueB64, msg.ValueEncoding)
			if !keyOK || !valueOK {
				// Schema-Registry-decoded key/value: the API only exposes the
				// decoded rendering, not the original wire-format bytes, so
				// this record cannot be reproduced verbatim. Skip rather than
				// silently corrupt it or abort the whole copy.
				skipped++
				continue
			}

			produceReq := kafkapkg.ProduceRequest{
				Partition:     destPartition,
				Key:           key,
				Value:         value,
				KeyEncoding:   keyEncoding,
				ValueEncoding: valueEncoding,
				Headers:       msg.Headers,
			}

			injectKafkitoProduceHeaders(&produceReq, user)

			produceCtx, produceCancel := context.WithTimeout(opCtx, 10*time.Second)
			_, produceErr := a.reg.Produce(produceCtx, destCluster, req.DestTopic, produceReq)
			produceCancel()

			if produceErr != nil {
				if errors.Is(produceErr, context.Canceled) {
					return
				}
				sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true, Error: "produce: " + produceErr.Error()})
				return
			}

			copied++

			// Emit a progress heartbeat every 100 records.
			if copied%100 == 0 {
				if !sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped}) {
					return // client disconnected
				}
			}

			if req.Limit > 0 && copied >= req.Limit {
				goto done
			}
		}

		if !page.HasMore || page.NextCursor == nil {
			break
		}

		// Drop partitions that have already crossed req.ToTSMs so the next
		// page doesn't keep re-fetching (and re-checking) them; once every
		// partition in the cursor is done, the copy is complete.
		if donePartitions != nil {
			for p := range page.NextCursor.Partitions {
				if donePartitions[p] {
					delete(page.NextCursor.Partitions, p)
				}
			}
			if len(page.NextCursor.Partitions) == 0 {
				break
			}
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

done:
	sendEvent(copyProgressEvent{Copied: copied, Skipped: skipped, Done: true})
}

// produceEncodingFor maps a consumed field's (rendered, base64, encoding)
// triple — as returned by the consumer for Message.Key/Value — to the
// (value, encoding) pair kafkapkg.ProduceRequest expects, so the record can
// be reproduced byte-for-byte on the destination.
//
// "binary" payloads only retain their original bytes in the base64 form, so
// those use ProduceRequest's "base64" encoding; everything else ("null",
// "empty", "text", "json") round-trips exactly through the rendered string
// as-is via "text". Schema-Registry-decoded fields ("avro", "json_schema",
// "protobuf") are reported as not-ok: applySRDecoder overwrites the raw
// bytes with the decoded JSON rendering and discards the base64 form, so the
// original wire-format bytes are not recoverable from a Message at all.
func produceEncodingFor(rendered, b64, encoding string) (value, produceEncoding string, ok bool) {
	switch encoding {
	case "avro", "json_schema", "protobuf":
		return "", "", false
	case "binary":
		return b64, "base64", true
	default: // "null", "empty", "json", "text"
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
