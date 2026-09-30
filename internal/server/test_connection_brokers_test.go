// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// fakeClusters answers the calls TestCluster makes. ping and probe are the
// results of Ping and ProbeBrokers, skipped is the number of brokers
// ProbeBrokers reports as not dialled; probed records whether ProbeBrokers
// ran.
type fakeClusters struct {
	clusterStore
	ping    error
	issues  []kafkapkg.BrokerIssue
	skipped int
	probe   error
	probed  *bool
}

func (f fakeClusters) UseAdhoc(config.ClusterConfig) (string, error) { return "__adhoc_fake", nil }

func (f fakeClusters) Client(string) (*kgo.Client, error) { return nil, nil }

func (f fakeClusters) Ping(context.Context, string) error { return f.ping }

func (f fakeClusters) ProbeBrokers(context.Context, string) ([]kafkapkg.BrokerIssue, int, error) {
	if f.probed != nil {
		*f.probed = true
	}
	return f.issues, f.skipped, f.probe
}

func (f fakeClusters) Capabilities(context.Context, string) (*kafkapkg.Capabilities, error) {
	return &kafkapkg.Capabilities{ListTopics: true}, nil
}

const testClusterBody = `{"brokers":["` + unreachableBroker + `"]}`

func postTestCluster(t *testing.T, fc fakeClusters) kafkapkg.ClusterInfo {
	t.Helper()
	h := fakeServer(t, stores{clusters: fc})
	tc := requestCase{
		name: "test", method: http.MethodPost, path: "/api/v1/clusters/_test",
		contentType: "application/json", body: testClusterBody, wantStatus: http.StatusOK,
	}
	req, rec := tc.do(t, h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assertResponseMatchesSpec(t, contractRouter(t), req, rec)
	var info kafkapkg.ClusterInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	return info
}

var blockedLocalhostIssue = kafkapkg.BrokerIssue{
	NodeID: 1, Host: "localhost", Port: 39092, Reason: kafkapkg.BrokerIssueBlocked,
	Error: `unable to dial: dial refused: address resolves to a blocked range: host "localhost" -> ::1`,
}

// Issue #126: the seed answers, but a broker advertises a blocked address.
// Test connection must not report the cluster as reachable.
func TestTestCluster_BrokerIssuesMakeClusterUnreachable(t *testing.T) {
	t.Parallel()

	issues := []kafkapkg.BrokerIssue{blockedLocalhostIssue}

	info := postTestCluster(t, fakeClusters{issues: issues})

	assert.False(t, info.Reachable)
	assert.Equal(t, issues, info.BrokerIssues)
	assert.Zero(t, info.BrokersSkipped)
	assert.Equal(t, "some advertised brokers cannot be reached: broker 1 advertises localhost:39092 (blocked)", info.Error)
	assert.Nil(t, info.Capabilities, "no capability probe for a cluster that is not usable")
}

func TestTestCluster_AllBrokersReachable(t *testing.T) {
	t.Parallel()

	info := postTestCluster(t, fakeClusters{})

	assert.True(t, info.Reachable)
	assert.Empty(t, info.BrokerIssues)
	assert.Zero(t, info.BrokersSkipped)
	assert.Empty(t, info.Error)
	require.NotNil(t, info.Capabilities)
}

// Brokers beyond the probe cap were not checked. That alone does not make
// the cluster unreachable, but the response says the result is partial.
func TestTestCluster_SkippedBrokersAreReported(t *testing.T) {
	t.Parallel()

	info := postTestCluster(t, fakeClusters{skipped: 36})

	assert.True(t, info.Reachable)
	assert.Empty(t, info.BrokerIssues)
	assert.Equal(t, 36, info.BrokersSkipped)
	assert.Empty(t, info.Error)
	require.NotNil(t, info.Capabilities)
}

// The skipped note matches the frontend's SkippedNote wording, singular for
// one broker.
func TestTestCluster_SkippedBrokersAreNamedInTheSummary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		skipped  int
		wantNote string
	}{
		{1, "1 more broker was not checked"},
		{36, "36 more brokers were not checked"},
	}
	for _, tc := range cases {
		t.Run(tc.wantNote, func(t *testing.T) {
			t.Parallel()

			issues := []kafkapkg.BrokerIssue{blockedLocalhostIssue}

			info := postTestCluster(t, fakeClusters{issues: issues, skipped: tc.skipped})

			assert.False(t, info.Reachable)
			assert.Equal(t, issues, info.BrokerIssues)
			assert.Equal(t, tc.skipped, info.BrokersSkipped)
			assert.Equal(t, "some advertised brokers cannot be reached: broker 1 advertises localhost:39092 (blocked); "+
				tc.wantNote, info.Error)
		})
	}
}

func TestTestCluster_ProbeErrorIsReported(t *testing.T) {
	t.Parallel()

	info := postTestCluster(t, fakeClusters{probe: errors.New("broker list: i/o timeout")})

	assert.False(t, info.Reachable)
	assert.Empty(t, info.BrokerIssues)
	assert.Equal(t, "broker list: i/o timeout", info.Error)
}

// A failed seed ping is reported as before; the brokers are not probed.
func TestTestCluster_PingFailureSkipsBrokerProbe(t *testing.T) {
	t.Parallel()

	probed := false
	info := postTestCluster(t, fakeClusters{ping: errors.New("unable to dial"), probed: &probed})

	assert.False(t, info.Reachable)
	assert.Equal(t, "unable to dial", info.Error)
	assert.False(t, probed)
}
