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

const upstreamBody = `{"code":"kafka_upstream","error":"upstream kafka error"}`

// errBroker stands for a broker-side failure whose text (internal address)
// must not reach the client.
var errBroker = errors.New("dial tcp 10.0.0.7:9092: connection refused")

// TestCreateGroup_ErrorMapping pins how createGroup maps store errors,
// including the 403 branch an in-memory cluster cannot produce.
func TestCreateGroup_ErrorMapping(t *testing.T) {
	t.Parallel()

	const body = `{"group_id":"g1","topic":"orders","strategy":"latest"}`
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"unknown cluster", fmt.Errorf("%w: kf", kafkapkg.ErrUnknownCluster), http.StatusNotFound, `{"error":"unknown cluster: kf"}`},
		// The unknown-cluster check wins over the "not found" text rule.
		{"unknown cluster named not found", fmt.Errorf("cluster not found: %w", kafkapkg.ErrUnknownCluster), http.StatusNotFound, `{"error":"unknown cluster: kf"}`},
		{"group exists", kafkapkg.ErrGroupExists, http.StatusConflict, `{"error":"kafka: consumer group already exists"}`},
		{"not authorized", fmt.Errorf("%w (GROUP_AUTHORIZATION_FAILED)", kafkapkg.ErrNotAuthorized), http.StatusForbidden, `{"error":"` + kafkapkg.ErrNotAuthorized.Error() + ` (GROUP_AUTHORIZATION_FAILED)"}`},
		{"topic not found", errors.New(`create group: topic "orders" not found`), http.StatusBadRequest, `{"error":"kafka: create group: topic \"orders\" not found"}`},
		{"required field", errors.New("create group: topic required"), http.StatusBadRequest, `{"error":"kafka: create group: topic required"}`},
		{"broker failure", errBroker, http.StatusBadGateway, upstreamBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fakeServer(t, stores{groups: fakeGroups{
				createGroup: func(kafkapkg.CreateGroupRequest) (*kafkapkg.ResetOffsetsResult, error) { return nil, tc.err },
			}})
			runFakeCases(t, h, []requestCase{{
				name: tc.name, method: http.MethodPost, path: "/api/v1/clusters/kf/groups",
				contentType: "application/json", body: body,
				wantStatus: tc.wantStatus, wantBody: tc.wantBody,
			}})
		})
	}
}

// TestCreateGroup_PassesBodyToStore pins that the validated body reaches
// the store unchanged and its result is returned as is.
func TestCreateGroup_PassesBodyToStore(t *testing.T) {
	t.Parallel()

	var got kafkapkg.CreateGroupRequest
	h := fakeServer(t, stores{groups: fakeGroups{
		createGroup: func(req kafkapkg.CreateGroupRequest) (*kafkapkg.ResetOffsetsResult, error) {
			got = req
			return &kafkapkg.ResetOffsetsResult{Group: req.GroupID, Topic: req.Topic, DryRun: req.DryRun, Results: []kafkapkg.ResetOffsetResult{}}, nil
		},
	}})
	runFakeCases(t, h, []requestCase{{
		name: "create", method: http.MethodPost, path: "/api/v1/clusters/kf/groups",
		contentType: "application/json", body: `{"group_id":"g1","topic":"orders","strategy":"offset","offset":7,"dry_run":true}`,
		wantStatus: http.StatusOK, wantBody: `{"group":"g1","topic":"orders","dry_run":true,"results":[]}`,
	}})
	assert.Equal(t, "g1", got.GroupID)
	assert.Equal(t, "orders", got.Topic)
	assert.Equal(t, kafkapkg.ResetOffsetStrategy("offset"), got.Strategy)
	assert.EqualValues(t, 7, got.Offset)
	assert.True(t, got.DryRun)
}

// TestResetGroupOffsets_ProdConfirmation pins that a production cluster
// rejects the reset before the store is called unless the confirmation
// header is set.
func TestResetGroupOffsets_ProdConfirmation(t *testing.T) {
	t.Parallel()

	calls := 0
	h := fakeServer(t, stores{
		configs: fakeConfigs{"prod": {Name: "prod", IsProd: true}},
		groups: fakeGroups{resetOffsets: func(group string, req kafkapkg.ResetOffsetsRequest) (*kafkapkg.ResetOffsetsResult, error) {
			calls++
			return &kafkapkg.ResetOffsetsResult{Group: group, Topic: req.Topic, Results: []kafkapkg.ResetOffsetResult{}}, nil
		}},
	})
	const path = "/api/v1/clusters/prod/groups/g1/reset-offsets"
	const body = `{"topic":"orders","strategy":"latest"}`
	runFakeCases(t, h, []requestCase{
		{name: "without confirmation", method: http.MethodPost, path: path, contentType: "application/json", body: body,
			wantStatus: http.StatusPreconditionRequired, wantBody: `{"code":"production_confirmation_required","error":"production cluster: resend with X-Kafkito-Confirm-Prod: true after user confirmation"}`},
	})
	assert.Zero(t, calls, "store must not be called without confirmation")

	runFakeCases(t, h, []requestCase{
		{name: "confirmed", method: http.MethodPost, path: path, contentType: "application/json", body: body,
			header: map[string]string{ProdConfirmHeader: "true"}, wantStatus: http.StatusOK, wantBody: `"group":"g1"`},
	})
	assert.Equal(t, 1, calls)
}

// TestResetGroupOffsets_ErrorMapping pins how resetGroupOffsets maps store
// errors.
func TestResetGroupOffsets_ErrorMapping(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"unknown cluster named not found", fmt.Errorf("cluster not found: %w", kafkapkg.ErrUnknownCluster), http.StatusNotFound, `{"error":"unknown cluster: kf"}`},
		{"required field", errors.New("reset offsets: topic required"), http.StatusBadRequest, `{"error":"kafka: reset offsets: topic required"}`},
		{"unknown strategy", errors.New("reset offsets: unknown strategy"), http.StatusBadRequest, `{"error":"kafka: reset offsets: unknown strategy"}`},
		{"group not found", errors.New(`group "g1" not found`), http.StatusBadRequest, `{"error":"kafka: group \"g1\" not found"}`},
		// Unlike createGroup, resetGroupOffsets has no 403 branch.
		{"not authorized", kafkapkg.ErrNotAuthorized, http.StatusBadGateway, upstreamBody},
		{"broker failure", errBroker, http.StatusBadGateway, upstreamBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fakeServer(t, stores{
				configs: fakeConfigs{"kf": {Name: "kf"}},
				groups: fakeGroups{resetOffsets: func(string, kafkapkg.ResetOffsetsRequest) (*kafkapkg.ResetOffsetsResult, error) {
					return nil, tc.err
				}},
			})
			runFakeCases(t, h, []requestCase{{
				name: tc.name, method: http.MethodPost, path: "/api/v1/clusters/kf/groups/g1/reset-offsets",
				contentType: "application/json", body: `{"topic":"orders","strategy":"latest"}`,
				wantStatus: tc.wantStatus, wantBody: tc.wantBody,
			}})
		})
	}
}
