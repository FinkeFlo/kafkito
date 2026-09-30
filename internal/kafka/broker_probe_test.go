// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/netguard"
)

// probeSeedHost is the seed a private cluster is configured with in these
// tests. The fake resolver maps it to probeSeedIP (TEST-NET-1, allowed by the
// guard) and the fake dial redirects that IP to the local kfake listener.
const (
	probeSeedHost = "seed.kafkito.test"
	probeSeedIP   = "192.0.2.10"
)

// advertisedAs wraps a listener so that kfake advertises ip instead of
// 127.0.0.1 in its metadata; the socket itself stays on loopback.
type advertisedAs struct {
	net.Listener
	ip net.IP
}

func (l advertisedAs) Addr() net.Addr {
	a := l.Listener.Addr().(*net.TCPAddr)
	return &net.TCPAddr{IP: l.ip, Port: a.Port}
}

// probeFixture is a kfake cluster behind a private (ad-hoc) cluster whose
// guarded dial is the real netguard policy with a fake resolver and dial.
type probeFixture struct {
	conns *Connections
	name  string
}

// newProbeFixture starts one kfake broker per entry of advertise. An empty
// entry keeps kfake's own 127.0.0.1 (blocked by the guard); an IP is
// advertised instead. IPs listed in reachable are redirected to the broker's
// loopback socket; any other allowed IP fails like a refused connection.
func newProbeFixture(t *testing.T, advertise []string, reachable map[string]bool) *probeFixture {
	t.Helper()

	var mu sync.Mutex
	next := 0
	loopback := map[string]string{} // advertised IP -> 127.0.0.1:port
	c, err := kfake.NewCluster(
		kfake.NumBrokers(len(advertise)),
		kfake.ListenFn(func(network, address string) (net.Listener, error) {
			ln, err := net.Listen(network, address)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			defer mu.Unlock()
			ip := advertise[next]
			next++
			if ip == "" {
				return ln, nil
			}
			loopback[ip] = ln.Addr().String()
			return advertisedAs{Listener: ln, ip: net.ParseIP(ip)}, nil
		}),
	)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	// The seed is the first broker, reached through its own socket.
	seedPort := c.ListenAddrs()[0]
	_, port, err := net.SplitHostPort(seedPort)
	require.NoError(t, err)
	mu.Lock()
	if advertise[0] == "" {
		loopback[probeSeedIP] = seedPort
	} else {
		loopback[probeSeedIP] = loopback[advertise[0]]
	}
	mu.Unlock()

	resolve := func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == probeSeedHost {
			return []netip.Addr{netip.MustParseAddr(probeSeedIP)}, nil
		}
		if a, err := netip.ParseAddr(host); err == nil {
			return []netip.Addr{a}, nil
		}
		return nil, fmt.Errorf("lookup %s: no such host", host)
	}
	dialOne := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		target, ok := loopback[host]
		mu.Unlock()
		if ok && (host == probeSeedIP || reachable[host]) {
			var d net.Dialer
			return d.DialContext(ctx, network, target)
		}
		return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
	}

	conns := newConnections(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	conns.adhocDial = netguard.GuardedDialWith(resolve, dialOne)
	t.Cleanup(conns.closeClients)
	name, err := conns.UseAdhoc(config.ClusterConfig{Brokers: []string{net.JoinHostPort(probeSeedHost, port)}})
	require.NoError(t, err)
	return &probeFixture{conns: conns, name: name}
}

func (f *probeFixture) probe(t *testing.T) []BrokerIssue {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, f.conns.Ping(ctx, f.name), "the seed must answer")
	issues, err := f.conns.ProbeBrokers(ctx, f.name)
	require.NoError(t, err)
	return issues
}

// Issue #126: a seed on an allowed address passes Ping even though the
// broker advertises a loopback address the guard refuses. ProbeBrokers
// reports it.
func TestProbeBrokers_ReportsBlockedAdvertisedBroker(t *testing.T) {
	t.Parallel()

	f := newProbeFixture(t, []string{""}, nil)

	issues := f.probe(t)

	require.Len(t, issues, 1)
	is := issues[0]
	assert.Equal(t, int32(0), is.NodeID)
	assert.Equal(t, "127.0.0.1", is.Host)
	assert.NotZero(t, is.Port)
	assert.Equal(t, BrokerIssueBlocked, is.Reason)
	assert.Contains(t, is.Error, netguard.ErrBlockedAddress.Error())
}

func TestProbeBrokers_AllReachable(t *testing.T) {
	t.Parallel()

	f := newProbeFixture(t,
		[]string{"192.0.2.20", "192.0.2.21"},
		map[string]bool{"192.0.2.20": true, "192.0.2.21": true})

	assert.Empty(t, f.probe(t))
}

// Every advertised broker is probed, not only the first failure, and an
// allowed but dead address is "unreachable", not "blocked".
func TestProbeBrokers_ReportsEachBrokerWithItsReason(t *testing.T) {
	t.Parallel()

	f := newProbeFixture(t,
		[]string{"192.0.2.20", "192.0.2.21", ""},
		map[string]bool{"192.0.2.20": true})

	issues := f.probe(t)

	require.Len(t, issues, 2)
	assert.Equal(t, int32(1), issues[0].NodeID)
	assert.Equal(t, "192.0.2.21", issues[0].Host)
	assert.Equal(t, BrokerIssueUnreachable, issues[0].Reason)
	assert.NotContains(t, issues[0].Error, netguard.ErrBlockedAddress.Error())
	assert.Equal(t, int32(2), issues[1].NodeID)
	assert.Equal(t, "127.0.0.1", issues[1].Host)
	assert.Equal(t, BrokerIssueBlocked, issues[1].Reason)
}

func TestProbeBrokers_UnknownCluster(t *testing.T) {
	t.Parallel()

	conns := newConnections(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := conns.ProbeBrokers(context.Background(), "nope")
	assert.ErrorIs(t, err, ErrUnknownCluster)
}
