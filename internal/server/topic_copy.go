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
	Copied int64  `json:"copied"`
	Done   bool   `json:"done,omitempty"`
	Error  string `json:"error,omitempty"`
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

	// Resolve destination cluster name.
	destCluster, err := a.resolveDestCluster(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Prod-cluster check for the DESTINATION.
	if !a.requireProdConfirmation(w, r, destCluster) {
		return
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

	// Copy loop.
	var (
		copied      int64
		cursor      *string
		from        kafkapkg.ConsumeFrom
		partitionOpt int32 = -1
	)

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
				sendEvent(copyProgressEvent{Copied: copied, Done: true, Error: "cursor decode: " + decErr.Error()})
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
			sendEvent(copyProgressEvent{Copied: copied, Done: true, Error: "consume: " + consumeErr.Error()})
			return
		}

		for _, msg := range page.Messages {
			// Filter by ToTSMs when no cursor yet (first batch from FromTimestamp/FromStart
			// doesn't enforce an upper bound at the kafka level for all cases).
			if req.ToTSMs > 0 && msg.Timestamp > req.ToTSMs {
				// We've passed the requested end timestamp; stop.
				goto done
			}

			destPartition := (*int32)(nil)
			if req.PreservePartition {
				p := msg.Partition
				destPartition = &p
			}

			produceReq := kafkapkg.ProduceRequest{
				Partition:     destPartition,
				Key:           msg.Key,
				Value:         msg.Value,
				KeyEncoding:   msg.KeyEncoding,
				ValueEncoding: msg.ValueEncoding,
				Headers:       msg.Headers,
			}

			user := rbacSubject(r, a.policy)
			injectKafkitoProduceHeaders(&produceReq, user)

			produceCtx, produceCancel := context.WithTimeout(opCtx, 10*time.Second)
			_, produceErr := a.reg.Produce(produceCtx, destCluster, req.DestTopic, produceReq)
			produceCancel()

			if produceErr != nil {
				if errors.Is(produceErr, context.Canceled) {
					return
				}
				sendEvent(copyProgressEvent{Copied: copied, Done: true, Error: "produce: " + produceErr.Error()})
				return
			}

			copied++

			// Emit a progress heartbeat every 100 records.
			if copied%100 == 0 {
				if !sendEvent(copyProgressEvent{Copied: copied}) {
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

		// Advance the cursor for the next iteration.
		encoded, encErr := kafkapkg.EncodeCursor(*page.NextCursor)
		if encErr != nil {
			sendEvent(copyProgressEvent{Copied: copied, Done: true, Error: "cursor encode: " + encErr.Error()})
			return
		}
		cursor = &encoded
		// Subsequent pages use cursor-based offset seek, not timestamp.
		from = kafkapkg.FromOffset
	}

done:
	sendEvent(copyProgressEvent{Copied: copied, Done: true})
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
