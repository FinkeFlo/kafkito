// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// apiError is an error with a client-facing representation: the HTTP status
// and the documented Error body (`{"error": Message, "code": Code}`). Err is
// the underlying cause. It is logged for 5xx responses and never sent to the
// client, so it may carry upstream details (hostnames, broker errors).
type apiError struct {
	Status  int
	Code    string
	Message string
	// Op names the failed operation in the server log (5xx only).
	Op  string
	Err error
}

func (e *apiError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *apiError) Unwrap() error { return e.Err }

func badRequest(msg string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Message: msg}
}

// upstreamError reports a failed broker or Schema Registry call. The client
// gets a generic 502 so internal hostnames and topology are not leaked; the
// full error is logged with op as context.
func upstreamError(op string, err error) *apiError {
	return &apiError{
		Status:  http.StatusBadGateway,
		Code:    "kafka_upstream",
		Message: "upstream kafka error",
		Op:      op,
		Err:     err,
	}
}

// clusterError classifies an error from a Registry call against cluster:
// an unknown cluster is a 404 naming it, anything else an upstream error.
func clusterError(cluster, op string, err error) error {
	if errors.Is(err, kafkapkg.ErrUnknownCluster) {
		return &apiError{Status: http.StatusNotFound, Message: "unknown cluster: " + cluster, Err: err}
	}
	return upstreamError(op, err)
}

// sentinelErrors maps domain sentinel errors to their client representation.
// Messages are the sentinels' own static texts, never the wrapped error chain.
var sentinelErrors = []struct {
	target error
	status int
	code   string
	msg    string
}{
	{kafkapkg.ErrUnknownCluster, http.StatusNotFound, "", kafkapkg.ErrUnknownCluster.Error()},
	{kafkapkg.ErrNoSchemaRegistry, http.StatusNotFound, "", kafkapkg.ErrNoSchemaRegistry.Error()},
	{kafkapkg.ErrTopicNotFound, http.StatusNotFound, "", kafkapkg.ErrTopicNotFound.Error()},
	{kafkapkg.ErrGroupExists, http.StatusConflict, "", "kafka: " + kafkapkg.ErrGroupExists.Error()},
	{kafkapkg.ErrNotAuthorized, http.StatusForbidden, "", kafkapkg.ErrNotAuthorized.Error()},
	{kafkapkg.ErrValueTooLarge, http.StatusRequestEntityTooLarge, "", fmt.Sprintf("value exceeds the %d MB download limit", kafkapkg.MaxRawDownloadMB)},
}

// toAPIError maps any error to its client representation. Unknown errors
// become an opaque 500.
func toAPIError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	var pe *paramError
	if errors.As(err, &pe) {
		return &apiError{Status: pe.status, Message: pe.msg}
	}
	if ve := requestValidationError(err); ve != nil {
		return ve
	}
	for _, s := range sentinelErrors {
		if errors.Is(err, s.target) {
			return &apiError{Status: s.status, Code: s.code, Message: s.msg, Err: err}
		}
	}
	return &apiError{Status: http.StatusInternalServerError, Message: "internal server error", Err: err}
}

// errorWriter is the single place that turns errors into HTTP responses. Its
// writeError method has the func(w, r, err) shape used by the generated
// server's error hooks and the request validator.
type errorWriter struct {
	log *slog.Logger
}

// writeError writes err as the documented JSON Error body. Only 5xx causes
// are logged: 4xx causes may echo request input (including the
// X-Kafkito-Cluster header) and are therefore never logged or returned.
func (ew errorWriter) writeError(w http.ResponseWriter, r *http.Request, err error) {
	ae := toAPIError(err)
	if ae.Status >= http.StatusInternalServerError && ew.log != nil {
		var attrs []any
		if ae.Op != "" {
			attrs = append(attrs, "detail", ae.Op)
		}
		if ae.Err != nil {
			attrs = append(attrs, "err", ae.Err)
		}
		ew.log.ErrorContext(r.Context(), ae.Message, attrs...)
	}
	body := map[string]string{"error": ae.Message}
	if ae.Code != "" {
		body["code"] = ae.Code
	}
	writeJSON(w, ae.Status, body)
}
