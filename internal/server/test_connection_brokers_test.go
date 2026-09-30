// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/connerr"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/netguard"
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
	info, _ := postTestClusterRaw(t, fc)
	return info
}

// postTestClusterRaw is postTestCluster that also returns the raw body.
func postTestClusterRaw(t *testing.T, fc fakeClusters) (kafkapkg.ClusterInfo, string) {
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
	return info, rec.Body.String()
}

// errBlockedLocalhostIssue is the error behind blockedLocalhostIssue. It
// names the address, so it reaches the server log only.
var errBlockedLocalhostIssue = fmt.Errorf(`unable to dial: %w: host "localhost" -> ::1`, netguard.ErrBlockedAddress)

var blockedLocalhostIssue = kafkapkg.BrokerIssue{
	NodeID: 1, Host: "localhost", Port: 39092, Reason: kafkapkg.BrokerIssueBlocked,
	Error: connerr.Blocked.Message(), ErrorClass: connerr.Blocked, Cause: errBlockedLocalhostIssue,
}

// asServed is issues as a client decodes them: Cause stays on the server.
func asServed(issues []kafkapkg.BrokerIssue) []kafkapkg.BrokerIssue {
	out := slices.Clone(issues)
	for i := range out {
		out[i].Cause = nil
	}
	return out
}

// Issue #126: the seed answers, but a broker advertises a blocked address.
// Test connection must not report the cluster as reachable.
func TestTestCluster_BrokerIssuesMakeClusterUnreachable(t *testing.T) {
	t.Parallel()

	issues := []kafkapkg.BrokerIssue{blockedLocalhostIssue}

	info := postTestCluster(t, fakeClusters{issues: issues})

	assert.False(t, info.Reachable)
	assert.Equal(t, asServed(issues), info.BrokerIssues)
	assert.Zero(t, info.BrokersSkipped)
	assert.Equal(t, "some advertised brokers cannot be reached: node 1: destination not allowed", info.Error)
	assert.Empty(t, info.ErrorClass, "each issue carries its own class")
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
			assert.Equal(t, asServed(issues), info.BrokerIssues)
			assert.Equal(t, tc.skipped, info.BrokersSkipped)
			assert.Equal(t, "some advertised brokers cannot be reached: node 1: destination not allowed; "+
				tc.wantNote, info.Error)
		})
	}
}

func TestTestCluster_ProbeErrorIsReported(t *testing.T) {
	t.Parallel()

	probe := fmt.Errorf("broker list: %w", &net.OpError{Op: "read", Net: "tcp", Addr: testClusterAddr, Err: os.ErrDeadlineExceeded})

	info, body := postTestClusterRaw(t, fakeClusters{probe: probe})

	assert.False(t, info.Reachable)
	assert.Empty(t, info.BrokerIssues)
	assert.Equal(t, "connection timed out", info.Error)
	assert.Equal(t, connerr.Timeout, info.ErrorClass)
	assert.NotContains(t, body, "192.0.2.1")
}

// A failed seed ping is reported by its class; the brokers are not probed.
func TestTestCluster_PingFailureSkipsBrokerProbe(t *testing.T) {
	t.Parallel()

	probed := false
	info := postTestCluster(t, fakeClusters{ping: errRefusedDial, probed: &probed})

	assert.False(t, info.Reachable)
	assert.Equal(t, "connection refused", info.Error)
	assert.Equal(t, connerr.Refused, info.ErrorClass)
	assert.False(t, probed)
}

// testClusterAddr is the broker address in the connection errors below.
var testClusterAddr = &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 9092}

// errRefusedDial is a connection error as franz-go reports it.
var errRefusedDial = fmt.Errorf("unable to dial: %w", &net.OpError{
	Op: "dial", Net: "tcp", Addr: testClusterAddr, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
})

// A failed Test connection names the class of the failure with its fixed
// text. The raw error stays out of the response: it names the address, the
// port, the resolver and the operating system error the server saw.
func TestTestCluster_PingErrorsAreReportedByClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want connerr.Class
	}{
		{"refused", errRefusedDial, connerr.Refused},
		{"timeout", fmt.Errorf("unable to dial: %w", &net.OpError{
			Op: "dial", Net: "tcp", Addr: testClusterAddr, Err: os.ErrDeadlineExceeded,
		}), connerr.Timeout},
		{"dns", fmt.Errorf("unable to dial: %w", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{
			Err: "no such host", Name: "echo-marker.invalid", Server: "192.168.65.7:53", IsNotFound: true,
		}}), connerr.DNS},
		{"tls", fmt.Errorf("unable to dial: %w", connerr.WithClass(connerr.TLS,
			fmt.Errorf("tls handshake to 192.0.2.1:9092: %w", errors.New("remote error: tls: handshake failure")))), connerr.TLS},
		{"sasl", fmt.Errorf("SASL authentication failed for 192.0.2.1:9092: %w", kerr.SaslAuthenticationFailed), connerr.SASL},
		{"blocked", fmt.Errorf("unable to dial: %w: host \"echo-marker.test\" -> 169.254.169.254", netguard.ErrBlockedAddress), connerr.Blocked},
		{"other", fmt.Errorf("unable to dial: %w", &net.OpError{
			Op: "dial", Net: "tcp", Addr: testClusterAddr, Err: os.NewSyscallError("connect", syscall.ENETUNREACH),
		}), connerr.Unreachable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			info, body := postTestClusterRaw(t, fakeClusters{ping: tc.err})

			assert.False(t, info.Reachable)
			assert.Equal(t, tc.want, info.ErrorClass)
			assert.Equal(t, tc.want.Message(), info.Error)
			for _, raw := range []string{"192.0.2.1", "192.168.65.7", ":53", "9092", "echo-marker", "169.254", "dial tcp", "connect:", "unable to dial", "i/o timeout", "no such host", "tls:", "network is unreachable"} {
				assert.NotContains(t, body, raw)
			}
		})
	}
}

// The server log keeps what the response leaves out: the full error with
// the addresses (docs/architecture.md lists these lines).
func TestTestCluster_LogKeepsTheFullError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		fc       fakeClusters
		wantMsg  string
		wantText []string
	}{
		{"ping failed", fakeClusters{ping: errRefusedDial}, "testCluster ping failed",
			[]string{"dial tcp 192.0.2.1:9092: connect: connection refused"}},
		{"broker list failed", fakeClusters{probe: errRefusedDial}, "testCluster broker probe failed",
			[]string{"dial tcp 192.0.2.1:9092: connect: connection refused"}},
		{"broker probe failed", fakeClusters{issues: []kafkapkg.BrokerIssue{blockedLocalhostIssue}}, "testCluster broker probe failed",
			[]string{"broker 1 advertises localhost:39092 (blocked)", netguard.ErrBlockedAddress.Error()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logs := &syncBuffer{}
			h := New(Options{
				Version: "test", Logger: slog.New(slog.NewTextHandler(logs, nil)), Config: config.Defaults(),
				stores: &stores{clusters: tc.fc},
			})
			rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", testClusterBody, "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			lines := logLinesWith(logs.String(), tc.wantMsg)
			require.Len(t, lines, 1, logs.String())
			for _, want := range tc.wantText {
				assert.Contains(t, lines[0], want)
			}
		})
	}
}
