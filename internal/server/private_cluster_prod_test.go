// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// The production confirmation of a private cluster follows the is_prod flag
// of the definition sent with the request, whichever variant of the same
// connection the server registered first: the production variant needs the
// confirmation, the other one does not.
func TestPrivateCluster_ProdConfirmationFollowsTheDefinition(t *testing.T) {
	t.Parallel()
	plain := config.ClusterConfig{Brokers: []string{unreachableBroker}}
	prod := plain
	prod.IsProd = true

	cases := []struct {
		name  string
		order []config.ClusterConfig
	}{
		{name: "plain registered first", order: []config.ClusterConfig{plain, prod, plain}},
		{name: "prod registered first", order: []config.ClusterConfig{prod, plain, prod}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := slog.New(slog.DiscardHandler)
			reg := kafkapkg.NewRegistry(nil, logger)
			t.Cleanup(reg.Close)
			h := New(Options{Version: "test", Logger: logger, Registry: reg, Config: config.Defaults()})

			for _, cfg := range tc.order {
				rec := deletePrivateTopic(t, h, cfg, false)
				if !cfg.IsProd {
					assert.Equal(t, http.StatusBadGateway, rec.Code, "plain variant: %s", rec.Body)
					continue
				}
				assert.Equal(t, http.StatusPreconditionRequired, rec.Code, "prod variant: %s", rec.Body)
				assert.Contains(t, rec.Body.String(), `"code":"production_confirmation_required"`)
				confirmed := deletePrivateTopic(t, h, cfg, true)
				assert.Equal(t, http.StatusBadGateway, confirmed.Code, "confirmed prod variant: %s", confirmed.Body)
			}
		})
	}
}

// deletePrivateTopic deletes the topic "orders" on the private cluster cfg,
// with the production confirmation if confirm is set. The broker is
// unreachable, so a request that gets past the confirmation ends in a 502.
func deletePrivateTopic(t *testing.T, h http.Handler, cfg config.ClusterConfig, confirm bool) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	path := "/api/v1/clusters/" + config.PrivateClusterSentinel + "/topics/orders"
	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, path, nil)
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))
	if confirm {
		req.Header.Set(ProdConfirmHeader, "true")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
