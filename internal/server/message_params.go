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
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

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
	switch q.Get("from") {
	case "", "end":
		opts.From = kafkapkg.FromEnd
	case "start":
		opts.From = kafkapkg.FromStart
	case "offset":
		opts.From = kafkapkg.FromOffset
		s := q.Get("offset")
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return opts, badParam("invalid offset")
		}
		opts.Offset = v
	default:
		return opts, badParam("invalid from")
	}
	return opts, nil
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
