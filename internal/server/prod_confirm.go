// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"
	"strings"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// ProdConfirmHeader is the request header the frontend must set to "true"
// to perform a mutating/dangerous operation (produce, delete topic, delete
// records, reset offsets) against a cluster marked is_prod. This is the
// server-side enforcement point: the frontend's confirmation dialog is a UX
// nicety, not a security boundary — a direct API call without this header
// against a production cluster is always rejected, regardless of what the
// caller's local cluster list says.
const ProdConfirmHeader = "X-Kafkito-Confirm-Prod"

// prodConfirmationError returns a 428 Precondition Required error when the
// named cluster is marked is_prod and the caller did not set
// ProdConfirmHeader: true, and nil when the request may proceed. Unknown
// clusters are allowed through here; the caller's own lookup
// (Client/Admin/etc.) will report ErrUnknownCluster as usual.
func prodConfirmationError(reg interface {
	ConfigFor(name string) (config.ClusterConfig, bool)
}, cluster string, r *http.Request) *apiError {
	cfg, ok := reg.ConfigFor(cluster)
	if !ok || !cfg.IsProd {
		return nil
	}
	if strings.EqualFold(r.Header.Get(ProdConfirmHeader), "true") {
		return nil
	}
	return &apiError{
		Status:  http.StatusPreconditionRequired,
		Code:    "production_confirmation_required",
		Message: "production cluster: resend with " + ProdConfirmHeader + ": true after user confirmation",
	}
}
