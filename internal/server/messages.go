// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
	"github.com/twmb/franz-go/pkg/kerr"
)

// ConsumeMessages pulls a bounded page of messages from a topic. Paging and
// seek semantics are documented on the consumeMessages operation.
func (s *apiServer) ConsumeMessages(ctx context.Context, req gen.ConsumeMessagesRequestObject) (gen.ConsumeMessagesResponseObject, error) {
	opts, err := consumeOptions(req.Params)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	res, err := s.reg.ConsumeMessages(ctx, req.Cluster, req.Topic, opts)
	if err != nil {
		return nil, clusterError(req.Cluster, "consume messages", err)
	}
	resp := gen.ConsumeMessages200JSONResponse{
		Cluster:  req.Cluster,
		Topic:    req.Topic,
		Messages: res.Messages,
		HasMore:  res.HasMore,
	}
	if res.Partial {
		resp.Partial = &res.Partial
	}
	if res.NextCursor != nil {
		if c, encErr := kafkapkg.EncodeCursor(*res.NextCursor); encErr == nil {
			resp.NextCursor = &c
		}
	}
	return resp, nil
}

// rawMessageResponse serves the untouched value bytes of one record as an
// attachment. The generated response types cannot express the sniffed
// content type, so it writes the response itself.
type rawMessageResponse struct {
	filename    string
	contentType string
	value       []byte
}

func (r rawMessageResponse) VisitDownloadMessageRawResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", r.contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, r.filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(r.value)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.value) //nolint:gosec // G705: value is served as attachment (Content-Disposition: attachment), not rendered as HTML.
	return err
}

// DownloadMessageRaw returns the raw value bytes of a single record without
// any string/base64 conversion. Values larger than 15 MB are rejected with
// 413 so a single oversized record cannot exhaust process memory.
func (s *apiServer) DownloadMessageRaw(ctx context.Context, req gen.DownloadMessageRawRequestObject) (gen.DownloadMessageRawResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	raw, err := s.reg.FetchRawMessageValue(ctx, req.Cluster, req.Topic, req.Partition, req.Offset)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrValueTooLarge) {
			return nil, err
		}
		return nil, clusterError(req.Cluster, "download message raw", err)
	}
	return rawMessageResponse{
		filename:    fmt.Sprintf("%s-p%d-o%d.%s", req.Topic, req.Partition, req.Offset, raw.Extension),
		contentType: raw.ContentType,
		value:       raw.Value,
	}, nil
}

// CountMessages returns the approximate number of messages inside the
// selected range, resolved from per-partition offset deltas.
func (s *apiServer) CountMessages(ctx context.Context, req gen.CountMessagesRequestObject) (gen.CountMessagesResponseObject, error) {
	opts, err := countOptions(req.Params)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := s.reg.CountMessages(ctx, req.Cluster, req.Topic, opts)
	if err != nil {
		return nil, clusterError(req.Cluster, "count messages", err)
	}
	return gen.CountMessages200JSONResponse{
		Cluster:          req.Cluster,
		Topic:            req.Topic,
		TotalApproxCount: res.TotalApproxCount,
		Partitions:       res.Partitions,
		FromTsMs:         res.FromTSMs,
		ToTsMs:           res.ToTSMs,
	}, nil
}

// GetMessageTimeline returns the approximate number of messages produced in
// each fixed-width time slot of the selected range.
func (s *apiServer) GetMessageTimeline(ctx context.Context, req gen.GetMessageTimelineRequestObject) (gen.GetMessageTimelineResponseObject, error) {
	opts, err := timelineOptions(req.Params)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	res, err := s.reg.MessageTimeline(ctx, req.Cluster, req.Topic, opts)
	if err != nil {
		return nil, clusterError(req.Cluster, "message timeline", err)
	}
	return gen.GetMessageTimeline200JSONResponse{
		Cluster:  req.Cluster,
		Topic:    req.Topic,
		FromTsMs: res.FromTSMs,
		ToTsMs:   res.ToTSMs,
		SlotMs:   res.SlotMs,
		Slots:    res.Slots,
	}, nil
}

// SampleMessages returns the last n decoded messages for use as a structural
// sample by the topic-search path picker (from=end, n<=25).
func (s *apiServer) SampleMessages(ctx context.Context, req gen.SampleMessagesRequestObject) (gen.SampleMessagesResponseObject, error) {
	opts := sampleOptions(req.Params)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := s.reg.ConsumeMessages(ctx, req.Cluster, req.Topic, opts)
	if err != nil {
		return nil, clusterError(req.Cluster, "sample messages", err)
	}
	return gen.SampleMessages200JSONResponse{
		Cluster:   req.Cluster,
		Topic:     req.Topic,
		Messages:  res.Messages,
		SampledAt: time.Now().UnixMilli(),
	}, nil
}

// SearchMessages runs a bounded, budget-limited content search.
func (s *apiServer) SearchMessages(ctx context.Context, req gen.SearchMessagesRequestObject) (gen.SearchMessagesResponseObject, error) {
	opts, err := searchOptions(req.Body)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := s.reg.SearchMessages(ctx, req.Cluster, req.Topic, opts)
	if err != nil {
		if msg := err.Error(); !errors.Is(err, kafkapkg.ErrUnknownCluster) && isSearchClientErr(msg) {
			// The 400 body uses the raw message without the "kafka: " prefix
			// the sibling handlers add.
			return nil, badRequest(msg)
		}
		return nil, clusterError(req.Cluster, "search messages", err)
	}
	return gen.SearchMessages200JSONResponse{
		Cluster:  req.Cluster,
		Topic:    req.Topic,
		Messages: &res.Messages,
		Search:   res.Stats,
	}, nil
}

// maxProduceBodyBytes caps the decompressed JSON body of a single-record
// produce request. Exported as a value (not just inline) so the 413 message
// and any client-side pre-flight size check (the Replay dialog) can reference
// the same number.
//
// Sized to fit a base64-encoded value up to kafkapkg.ProducerBatchMaxBytes
// (10 MiB raw): base64 inflates raw bytes by ~4/3 (~13.3 MiB), plus the key,
// headers and JSON punctuation, so 15 MiB leaves comfortable headroom —
// matching the existing 15 MB raw-download cap elsewhere in the app.
const maxProduceBodyBytes = 15 << 20 // 15 MiB

// maxProduceCompressedBodyBytes bounds the bytes read off the wire before
// gzip decompression (Content-Encoding: gzip) when the client compresses a
// large produce body. Gzip essentially never expands well-formed input by
// more than a small constant, so this only needs modest headroom over
// maxProduceBodyBytes; it exists purely as a safety net against a gzip bomb
// (a tiny compressed stream that decompresses to something enormous) rather
// than as a real-world limit — the decompressed size is what actually caps
// what the caller can send, enforced separately by produceBody.
const maxProduceCompressedBodyBytes = maxProduceBodyBytes + (1 << 20) // +1 MiB

// produceBody reads the produce request body before the request validator
// does: it caps the bytes read off the wire, decompresses a
// Content-Encoding: gzip body, and caps the decompressed size. The validator
// and handler then see the plain JSON body without Content-Encoding; other
// encodings are read as plain JSON, as before the strict server.
func produceBody(errs errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.Body == http.NoBody {
				next.ServeHTTP(w, r)
				return
			}
			data, err := readProduceBody(w, r)
			if err != nil {
				errs.writeError(w, r, err)
				return
			}
			r.Header.Del("Content-Encoding")
			r.Body = io.NopCloser(bytes.NewReader(data))
			r.ContentLength = int64(len(data))
			r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
			next.ServeHTTP(w, r)
		})
	}
}

// readProduceBody reads the produce request body, transparently
// decompressing it when the client sent Content-Encoding: gzip (the frontend
// compresses large bodies to stay under proxy request-size limits). A
// malformed gzip stream is a 400; exceeding the wire or decompressed cap a
// 413.
func readProduceBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	src := io.Reader(http.MaxBytesReader(w, r.Body, maxProduceCompressedBodyBytes))
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(src)
		if err != nil {
			return nil, badRequest("invalid gzip body: " + err.Error())
		}
		defer gz.Close() //nolint:errcheck // read-only reader; nothing actionable on close error
		src = gz
	}

	tooLarge := &apiError{
		Status:  http.StatusRequestEntityTooLarge,
		Message: fmt.Sprintf("request body exceeds the %d MB produce limit", maxProduceBodyBytes/(1<<20)),
	}
	// Read one byte past the cap so an exactly-at-the-limit body doesn't get
	// mistaken for an oversized one, without ever buffering more than
	// maxProduceBodyBytes+1 bytes regardless of what the client claims.
	data, err := io.ReadAll(io.LimitReader(src, maxProduceBodyBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, tooLarge
		}
		return nil, badRequest("invalid body: " + err.Error())
	}
	if len(data) > maxProduceBodyBytes {
		return nil, tooLarge
	}
	return data, nil
}

// ProduceMessage produces a single record to a topic. The body limit and
// gzip decompression run before the request validator (see
// produceBody).
func (s *apiServer) ProduceMessage(ctx context.Context, req gen.ProduceMessageRequestObject) (gen.ProduceMessageResponseObject, error) {
	r := httpRequestFromContext(ctx)
	if err := prodConfirmationError(s.reg, req.Cluster, r); err != nil {
		return nil, err
	}

	body := *req.Body
	injectKafkitoProduceHeaders(&body, rbacSubject(r, s.policy))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := s.reg.Produce(ctx, req.Cluster, req.Topic, body)
	if err != nil {
		return nil, produceError(req.Cluster, req.Topic, body.Partition, err)
	}
	return gen.ProduceMessage200JSONResponse(*res), nil
}

// produceError classifies a failed produce. Encoding issues and unknown
// partitions are client errors, oversized records 413, missing broker ACLs
// 403; everything else is an upstream error.
func produceError(cluster, topic string, partition *int32, err error) error {
	if errors.Is(err, kafkapkg.ErrUnknownCluster) {
		return clusterError(cluster, "produce message", err)
	}
	msg := err.Error()
	// kgo phrases a rejected partition choice in terms of the index the
	// partitioner returned (kafkito's internal "reject" sentinel), which says
	// nothing useful to the caller — report the partition they asked for.
	// Producing without a partition cannot reach this branch.
	if isInvalidPartitionErr(msg) && partition != nil {
		return badRequest(fmt.Sprintf("partition %d does not exist on topic %q", *partition, topic))
	}
	if isClientProduceErr(msg) {
		return badRequest("kafka: " + msg)
	}
	// A record larger than kafkito's client-side batch cap or the broker's
	// max.message.bytes is a caller-actionable input problem, not an outage.
	if errors.Is(err, kerr.MessageTooLarge) {
		return &apiError{
			Status:  http.StatusRequestEntityTooLarge,
			Code:    "kafka_message_too_large",
			Message: fmt.Sprintf("message too large to produce to topic %q (limit is %d MB)", topic, kafkapkg.ProducerBatchMaxBytes/(1<<20)),
			Err:     err,
		}
	}
	// The connected credential lacks WRITE (or IdempotentWrite) ACLs on the
	// topic: an actionable permission problem, not an upstream outage.
	if kafkapkg.IsAuthorizationFailure(msg) {
		return &apiError{
			Status:  http.StatusForbidden,
			Code:    "kafka_not_authorized",
			Message: fmt.Sprintf("not authorized to produce to topic %q (check the cluster credential's ACLs)", topic),
			Err:     err,
		}
	}
	return upstreamError("produce message", err)
}

func injectKafkitoProduceHeaders(req *kafkapkg.ProduceRequest, user string) {
	if req.Headers == nil {
		req.Headers = make(map[string]string)
	}
	req.Headers["X-Kafkito-Source"] = "true"
	if user != "" {
		req.Headers["X-Kafkito-User"] = user
	}
}

func isClientProduceErr(msg string) bool {
	return strings.Contains(msg, "invalid base64") ||
		strings.Contains(msg, "unsupported encoding")
}

// isInvalidPartitionErr reports whether a produce failed because the requested
// partition does not exist on the topic. franz-go raises this from the
// partitioner (see internal/kafka's explicitOrKeyPartitioner), so the wording comes
// from kgo rather than kafkito.
func isInvalidPartitionErr(msg string) bool {
	return strings.Contains(msg, "invalid record partitioning choice")
}

// isSearchClientErr reports whether a search error originates from bad
// caller-supplied input (400) rather than a broker-side failure (502).
// Used by searchMessages.
func isSearchClientErr(msg string) bool {
	return strings.Contains(msg, "jsonpath") || strings.Contains(msg, "xpath") ||
		strings.Contains(msg, "regex") || strings.Contains(msg, "numeric op") ||
		strings.Contains(msg, "unknown search mode") || strings.Contains(msg, "unknown operator") ||
		strings.Contains(msg, "js filter")
}
