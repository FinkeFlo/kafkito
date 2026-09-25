// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"sort"
)

// BrokerInfo is the per-broker view returned by ListBrokers.
//
// Only fields the Kafka admin API can return natively are populated.
// Resource metrics (CPU, disk, heap, connections) are out of scope until
// kafkito has a JMX scraper or per-broker metrics endpoint.
type BrokerInfo struct {
	NodeID       int32  `json:"node_id"`
	Host         string `json:"host"`
	Port         int32  `json:"port"`
	Rack         string `json:"rack,omitempty"`
	IsController bool   `json:"is_controller"`
}

// ListBrokers returns the brokers of the named cluster, sorted by NodeID.
// The controller flag is set on the broker whose ID matches the cluster's
// reported controller; if the controller is unknown (-1), no broker is
// flagged.
func (r *Registry) ListBrokers(ctx context.Context, name string) ([]BrokerInfo, error) {
	adm, err := r.Admin(name)
	if err != nil {
		return nil, err
	}
	md, err := adm.Metadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	out := make([]BrokerInfo, 0, len(md.Brokers))
	for _, b := range md.Brokers {
		rack := ""
		if b.Rack != nil {
			rack = *b.Rack
		}
		out = append(out, BrokerInfo{
			NodeID:       b.NodeID,
			Host:         b.Host,
			Port:         b.Port,
			Rack:         rack,
			IsController: md.Controller == b.NodeID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}
