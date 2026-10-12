// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// A search the options cannot express is a 400 with its text; any other
// store error is the generic 502, even when its text names a search keyword
// (a topic name may carry one) next to a broker address.
func TestSearchMessages_ErrorMapping(t *testing.T) {
	t.Parallel()

	const path = "/api/v1/clusters/kf/topics/orders_header_key/messages/search"
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "invalid search",
			err:        fmt.Errorf("%w: header search requires header_key", kafkapkg.ErrInvalidSearch),
			wantStatus: http.StatusBadRequest,
			wantBody:   `"error":"invalid search: header search requires header_key"`,
		},
		{
			name:       "broker error naming a keyword",
			err:        errors.New(`fetch metadata for topic "orders_header_key" regex xpath jsonpath: dial tcp broker-1.internal:9092: connection refused`),
			wantStatus: http.StatusBadGateway,
			wantBody:   `"code":"kafka_upstream"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fakeServer(t, stores{messages: fakeMessages{search: func(kafkapkg.SearchOptions) (*kafkapkg.SearchResult, error) {
				return nil, tc.err
			}}})
			_, rec := requestCase{method: http.MethodPost, path: path, contentType: "application/json", body: `{"mode":"key","op":"exists"}`}.do(t, h)
			assert.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.wantBody)
			assert.NotContains(t, rec.Body.String(), "broker-1.internal")
		})
	}
}
