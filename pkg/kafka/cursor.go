// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// CursorDirection enumerates the two paging directions the messages view
// supports. backward = newest→oldest (the default for from=end and time
// ranges traversed newest-first); forward = oldest→newest (from=start,
// from=offset, and time ranges traversed oldest-first).
type CursorDirection string

// Possible CursorDirection values.
const (
	CursorBackward CursorDirection = "backward"
	CursorForward  CursorDirection = "forward"
)

// Cursor is the in-memory representation of an opaque continuation token
// that points at the boundary between two pages of messages. It is encoded
// to a base64 string before crossing the API boundary; clients treat the
// encoded form as opaque.
type Cursor struct {
	// Partitions maps partition → next-page boundary offset. For a backward
	// cursor, the next page consumes records with offset strictly less than
	// this value per partition. For a forward cursor, the next page starts
	// at this offset (inclusive).
	Partitions map[int32]int64 `json:"p"`
	// Direction selects the seek direction used by the next page.
	Direction CursorDirection `json:"d"`
}

// cursorWire is the on-wire shape; partition keys must be JSON strings
// because JSON object keys are always strings.
type cursorWire struct {
	P map[string]int64 `json:"p"`
	D CursorDirection  `json:"d"`
}

// EncodeCursor serializes a Cursor to a base64 string suitable for use as
// a query parameter value. Returns an error when the direction is missing
// or invalid.
func EncodeCursor(c Cursor) (string, error) {
	if c.Direction != CursorBackward && c.Direction != CursorForward {
		return "", fmt.Errorf("invalid direction: %q", c.Direction)
	}
	w := cursorWire{
		P: make(map[string]int64, len(c.Partitions)),
		D: c.Direction,
	}
	for p, off := range c.Partitions {
		w.P[strconv.FormatInt(int64(p), 10)] = off
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// DecodeCursor parses a base64-encoded cursor string. Returns an error
// with a human-readable explanation suitable for surfacing as an HTTP 400.
// Both std and URL-safe base64 variants are accepted to keep deep links
// forgiving when a cursor crosses URL-encoding boundaries.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, errors.New("empty cursor")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return Cursor{}, fmt.Errorf("decode base64: %w", err)
		}
	}
	var w cursorWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Cursor{}, fmt.Errorf("decode json: %w", err)
	}
	if w.D != CursorBackward && w.D != CursorForward {
		return Cursor{}, fmt.Errorf("invalid direction: %q", w.D)
	}
	if w.P == nil {
		return Cursor{}, errors.New("missing partitions")
	}
	out := Cursor{
		Direction:  w.D,
		Partitions: make(map[int32]int64, len(w.P)),
	}
	for k, off := range w.P {
		pn, err := strconv.ParseInt(k, 10, 32)
		if err != nil {
			return Cursor{}, fmt.Errorf("invalid partition key %q: %w", k, err)
		}
		out.Partitions[int32(pn)] = off
	}
	return out, nil
}
