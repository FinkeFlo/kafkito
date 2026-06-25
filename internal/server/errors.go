// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"log/slog"
	"net/http"
)

// gatewayError logs the full upstream error server-side (with a short context
// detail) and returns a generic 502 to the client, so internal broker/SR
// hostnames and topology are not leaked in the HTTP response.
func gatewayError(ctx context.Context, w http.ResponseWriter, log *slog.Logger, detail string, err error) {
	if log != nil {
		log.ErrorContext(ctx, "upstream kafka error", "detail", detail, "err", err)
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{
		"error": "upstream kafka error",
		"code":  "kafka_upstream",
	})
}
