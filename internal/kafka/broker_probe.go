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

// BrokerIssue is an advertised broker that a probe could not talk to.
type BrokerIssue struct {
	NodeID int32  `json:"node_id"`
	Host   string `json:"host"`
	Port   int32  `json:"port"`
	Reason string `json:"reason"`
	Error  string `json:"error"`
}

// ProbeBrokers asks a seed broker of the named cluster for the brokers it
// advertises and sends each of them a request through the cluster's own
// client, so the dial goes through the same guard, TLS and SASL setup as
// every later request. Brokers are probed in parallel under ctx. It returns
// the brokers that failed, sorted by node ID, or nil when all answered.
// The error is non-nil only when no seed returned the broker list.
func (r *Connections) ProbeBrokers(ctx context.Context, name string) ([]BrokerIssue, error) {
	cl, err := r.Client(name)
	if err != nil {
		return nil, err
	}
	brokers, err := seedBrokerList(ctx, cl)
	if err != nil {
		return nil, err
	}

	results := make([]*BrokerIssue, len(brokers))
	var wg sync.WaitGroup
	for i, b := range brokers {
		wg.Go(func() {
			req := kmsg.NewPtrMetadataRequest()
			req.Topics = []kmsg.MetadataRequestTopic{}
			if _, err := cl.Broker(int(b.NodeID)).Request(ctx, req); err != nil {
				reason := BrokerIssueUnreachable
				if errors.Is(err, netguard.ErrBlockedAddress) {
					reason = BrokerIssueBlocked
				}
				results[i] = &BrokerIssue{
					NodeID: b.NodeID, Host: b.Host, Port: b.Port,
					Reason: reason, Error: err.Error(),
				}
			}
		})
	}
	wg.Wait()

	var issues []BrokerIssue
	for _, is := range results {
		if is != nil {
			issues = append(issues, *is)
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].NodeID < issues[j].NodeID })
	return issues, nil
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
