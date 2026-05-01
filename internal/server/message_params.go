// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

// maxPartitionOffsetsEntries caps the comma-separated partition_offsets
// query value. A topic with thousands of partitions is exotic; this guard
// stops a malicious or malformed input from forcing quadratic admin work
// in the kafka layer.
const maxPartitionOffsetsEntries = 1024

// paramError is a client-visible parse failure carrying an HTTP status.
type paramError struct {
	status int
	msg    string
}

func (e *paramError) Error() string { return e.msg }

func badParam(msg string) *paramError { return &paramError{status: http.StatusBadRequest, msg: msg} }

// writeParamError writes a paramError as JSON and returns true if it handled the error.
func writeParamError(w http.ResponseWriter, err error) bool {
	var pe *paramError
	if errors.As(err, &pe) {
		writeJSON(w, pe.status, map[string]string{"error": pe.msg})
		return true
	}
	return false
}

// parseConsumeQuery maps URL query to ConsumeOptions.
// Returns a *paramError on bad input.
func parseConsumeQuery(q url.Values) (kafkapkg.ConsumeOptions, error) {
	opts := kafkapkg.ConsumeOptions{
		Partition: -1,
		Limit:     50,
		From:      kafkapkg.FromEnd,
		Timeout:   6 * time.Second,
	}
	if s := q.Get("partition"); s != "" {
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return opts, badParam("invalid partition")
		}
		opts.Partition = int32(v)
	}
	if s := q.Get("limit"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 {
			return opts, badParam("invalid limit")
		}
		opts.Limit = v
	}
	rawFrom := q.Get("from")
	switch rawFrom {
	case "", "end":
		opts.From = kafkapkg.FromEnd
	case "start":
		opts.From = kafkapkg.FromStart
	case "offset":
		opts.From = kafkapkg.FromOffset
	case "timestamp":
		opts.From = kafkapkg.FromTimestamp
	default:
		return opts, badParam("invalid from")
	}

	if s := q.Get("from_ts_ms"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || v < 0 {
			return opts, badParam("invalid from_ts_ms")
		}
		opts.FromTSMs = v
	}
	if s := q.Get("to_ts_ms"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || v < 0 {
			return opts, badParam("invalid to_ts_ms")
		}
		opts.ToTSMs = v
	}
	if opts.FromTSMs > 0 && opts.ToTSMs > 0 && opts.ToTSMs < opts.FromTSMs {
		return opts, badParam("to_ts_ms must be >= from_ts_ms")
	}

	if s := q.Get("partition_offsets"); s != "" {
		if opts.From != kafkapkg.FromOffset {
			return opts, badParam("partition_offsets requires from=offset")
		}
		offs, err := parsePartitionOffsets(s)
		if err != nil {
			return opts, badParam("invalid partition_offsets: " + err.Error())
		}
		opts.PartitionOffsets = offs
	}

	if opts.From == kafkapkg.FromOffset && len(opts.PartitionOffsets) == 0 {
		s := q.Get("offset")
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return opts, badParam("invalid offset")
		}
		opts.Offset = v
	}

	if s := q.Get("cursor"); s != "" {
		c, decodeErr := kafkapkg.DecodeCursor(s)
		if decodeErr != nil {
			return opts, badParam("invalid cursor: " + decodeErr.Error())
		}
		switch c.Direction {
		case kafkapkg.CursorBackward:
			if rawFrom != "" && rawFrom != "end" {
				return opts, badParam(fmt.Sprintf("cursor direction backward conflicts with from=%s", rawFrom))
			}
			opts.From = kafkapkg.FromEnd
			opts.CursorUpperBounds = c.Partitions
		case kafkapkg.CursorForward:
			if rawFrom == "end" {
				return opts, badParam("cursor direction forward conflicts with from=end")
			}
			opts.From = kafkapkg.FromOffset
			opts.PartitionOffsets = c.Partitions
		}
	}

	return opts, nil
}

// parsePartitionOffsets parses a "p:o,p:o,…" list of per-partition seek
// offsets. Used by the GET /messages endpoint when the caller supplies
// from=offset across all partitions without a continuation cursor.
func parsePartitionOffsets(s string) (map[int32]int64, error) {
	pairs := strings.Split(s, ",")
	if len(pairs) > maxPartitionOffsetsEntries {
		return nil, fmt.Errorf("too many entries (max %d)", maxPartitionOffsetsEntries)
	}
	out := make(map[int32]int64, len(pairs))
	for _, raw := range pairs {
		pair := strings.TrimSpace(raw)
		if pair == "" {
			return nil, fmt.Errorf("empty pair")
		}
		kv := strings.SplitN(pair, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid pair %q", pair)
		}
		pi, err := strconv.ParseInt(kv[0], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid partition %q", kv[0])
		}
		oi, err := strconv.ParseInt(kv[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid offset %q", kv[1])
		}
		if oi < 0 {
			return nil, fmt.Errorf("negative offset %d", oi)
		}
		if _, dup := out[int32(pi)]; dup {
			return nil, fmt.Errorf("duplicate partition %d", pi)
		}
		out[int32(pi)] = oi
	}
	return out, nil
}

// parseSampleQuery maps URL query to ConsumeOptions for the sample endpoint.
// Defaults: n=5, partition=-1, from=end. Caps n at 25, raises n<1 to 1.
func parseSampleQuery(q url.Values) (kafkapkg.ConsumeOptions, error) {
	opts := kafkapkg.ConsumeOptions{
		Partition: -1,
		Limit:     5,
		From:      kafkapkg.FromEnd,
		Timeout:   6 * time.Second,
	}
	if s := q.Get("partition"); s != "" {
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return opts, badParam("invalid partition")
		}
		opts.Partition = int32(v)
	}
	if s := q.Get("n"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			return opts, badParam("invalid n")
		}
		if v < 1 {
			v = 1
		}
		if v > 25 {
			v = 25
		}
		opts.Limit = v
	}
	return opts, nil
}

// searchRequestBody is the wire format for POST /topics/{topic}/messages/search.
type searchRequestBody struct {
	Partition   *int32           `json:"partition"`
	Limit       int              `json:"limit"`
	Budget      int              `json:"budget"`
	Direction   string           `json:"direction"`
	StopOnLimit *bool            `json:"stop_on_limit"`
	Mode        string           `json:"mode"`
	Path        string           `json:"path"`
	Op          string           `json:"op"`
	Value       string           `json:"value"`
	Zones       []string         `json:"zones"`
	FromTSMs    int64            `json:"from_ts_ms"`
	ToTSMs      int64            `json:"to_ts_ms"`
	Cursors     map[string]int64 `json:"cursors"`
}

// parseSearchBody decodes the request body and maps it to SearchOptions.
// Returns a *paramError on bad input.
func parseSearchBody(r *http.Request) (kafkapkg.SearchOptions, error) {
	var body searchRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return kafkapkg.SearchOptions{}, badParam("invalid json body: " + err.Error())
	}
	opts := kafkapkg.SearchOptions{
		Partition: -1,
		Limit:     body.Limit,
		Budget:    body.Budget,
		Direction: kafkapkg.SearchDirection(body.Direction),
		Mode:      kafkapkg.SearchMode(body.Mode),
		Path:      body.Path,
		Op:        kafkapkg.SearchOp(body.Op),
		Value:     body.Value,
		FromTS:    body.FromTSMs,
		ToTS:      body.ToTSMs,
		Timeout:   12 * time.Second,
	}
	if body.Partition != nil {
		opts.Partition = *body.Partition
	}
	opts.StopOnLimit = body.StopOnLimit == nil || *body.StopOnLimit
	for _, z := range body.Zones {
		opts.Zones = append(opts.Zones, kafkapkg.SearchZone(z))
	}
	if len(body.Cursors) > 0 {
		opts.Cursors = make(map[int32]int64, len(body.Cursors))
		for k, v := range body.Cursors {
			pn, err := strconv.ParseInt(k, 10, 32)
			if err != nil {
				return opts, badParam(fmt.Sprintf("invalid partition key in cursors: %q", k))
			}
			opts.Cursors[int32(pn)] = v
		}
	}
	return opts, nil
}
