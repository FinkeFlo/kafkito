// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/FinkeFlo/kafkito/internal/connerr"
	"github.com/FinkeFlo/kafkito/internal/netguard"
)

// Reasons a broker probe reports in BrokerIssue.Reason.
const (
	// BrokerIssueBlocked: the advertised address resolves to a range the
	// outbound address guard refuses for private clusters.
	BrokerIssueBlocked = "blocked"
	// BrokerIssueUnreachable: the address is allowed, but the broker did
	// not answer (connection refused, timeout, TLS or SASL failure, ...).
	BrokerIssueUnreachable = "unreachable"
)

// Bounds on ProbeBrokers. For a private cluster the broker list comes from
// a user-chosen seed, so without them one seed could make kafkito open
// thousands of connections at once or use it to scan arbitrary addresses.
const (
	// maxProbedBrokers is how many advertised brokers are dialled at most.
	maxProbedBrokers = 64
	// maxConcurrentBrokerProbes is how many probes run at the same time.
	maxConcurrentBrokerProbes = 8
)

// BrokerIssue is an advertised broker that a probe could not talk to.
type BrokerIssue struct {
	NodeID int32  `json:"node_id"`
	Host   string `json:"host"`
	Port   int32  `json:"port"`
	Reason string `json:"reason"`
	// Error is the fixed text of ErrorClass. It never names an address;
	// Host and Port do.
	Error      string        `json:"error"`
	ErrorClass connerr.Class `json:"error_class"`
	// Cause is the full error, for the server log only.
	Cause error `json:"-"`
}

// ProbeBrokers asks a seed broker of the named cluster for the brokers it
// advertises and sends each of them a request through the cluster's own
// client, so the dial goes through the same guard, TLS and SASL setup as
// every later request. The caller must run Ping first: it loads the
// client's broker list, without which cl.Broker cannot resolve a node ID
// (such a broker is then reported as unreachable). Host and port of each
// issue are the ones the seed's metadata advertises.
//
// Brokers are probed in node ID order, at most maxConcurrentBrokerProbes at
// a time, under ctx. Only the first maxProbedBrokers are dialled; skipped
// is the number of advertised brokers beyond that cap, which were neither
// dialled nor reported, so the caller can say the result is partial.
//
// It returns the brokers that failed, sorted by node ID, or nil when all
// probed brokers answered. The error is non-nil only when no seed returned
// the broker list.
func (r *Connections) ProbeBrokers(ctx context.Context, name string) (issues []BrokerIssue, skipped int, err error) {
	cl, err := r.Client(name)
	if err != nil {
		return nil, 0, err
	}
	brokers, err := seedBrokerList(ctx, cl)
	if err != nil {
		return nil, 0, err
	}

	sort.Slice(brokers, func(i, j int) bool { return brokers[i].NodeID < brokers[j].NodeID })
	if len(brokers) > maxProbedBrokers {
		skipped = len(brokers) - maxProbedBrokers
		brokers = brokers[:maxProbedBrokers]
	}

	results := make([]*BrokerIssue, len(brokers))
	sem := make(chan struct{}, maxConcurrentBrokerProbes)
	var wg sync.WaitGroup
	for i, b := range brokers {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			req := kmsg.NewPtrMetadataRequest()
			req.Topics = []kmsg.MetadataRequestTopic{}
			if _, err := cl.Broker(int(b.NodeID)).Request(ctx, req); err != nil {
				reason := BrokerIssueUnreachable
				if errors.Is(err, netguard.ErrBlockedAddress) {
					reason = BrokerIssueBlocked
				}
				results[i] = &BrokerIssue{
					NodeID: b.NodeID, Host: b.Host, Port: b.Port, Reason: reason,
					Error: connerr.Message(err), ErrorClass: connerr.Classify(err), Cause: err,
				}
			}
		})
	}
	wg.Wait()

	for _, is := range results {
		if is != nil {
			issues = append(issues, *is)
		}
	}
	// results follows brokers, which is sorted by node ID.
	return issues, skipped, nil
}

// seedBrokerList asks the seed brokers, one after another, for the brokers
// the cluster advertises. It talks to the seeds directly: a client-routed
// metadata request would prefer the already discovered brokers, which are
// exactly the addresses under test.
func seedBrokerList(ctx context.Context, cl *kgo.Client) ([]kmsg.MetadataResponseBroker, error) {
	req := kmsg.NewPtrMetadataRequest()
	req.Topics = []kmsg.MetadataRequestTopic{}
	var lastErr error
	for _, seed := range cl.SeedBrokers() {
		resp, err := seed.Request(ctx, req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		return resp.(*kmsg.MetadataResponse).Brokers, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no seed brokers")
	}
	return nil, fmt.Errorf("broker list: %w", lastErr)
}
