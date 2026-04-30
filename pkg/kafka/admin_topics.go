// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/twmb/franz-go/pkg/kadm"
)

// --- Topic admin --------------------------------------------------------------

// CreateTopicRequest describes a new topic.
type CreateTopicRequest struct {
	Name              string            `json:"name"`
	Partitions        int32             `json:"partitions"`         // -1 = broker default
	ReplicationFactor int16             `json:"replication_factor"` // -1 = broker default
	Configs           map[string]string `json:"configs,omitempty"`
}

// CreateTopic creates a topic; returns an error if it already exists or the
// broker rejects the request.
func (r *Registry) CreateTopic(ctx context.Context, cluster string, req CreateTopicRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return errors.New("topic name required")
	}
	adm, err := r.Admin(cluster)
	if err != nil {
		return err
	}
	var cfgs map[string]*string
	if len(req.Configs) > 0 {
		cfgs = make(map[string]*string, len(req.Configs))
		for k, v := range req.Configs {
			vv := v
			cfgs[k] = &vv
		}
	}
	parts := req.Partitions
	if parts == 0 {
		parts = -1
	}
	rf := req.ReplicationFactor
	if rf == 0 {
		rf = -1
	}
	resp, err := adm.CreateTopic(ctx, parts, rf, cfgs, req.Name)
	if err != nil {
		return fmt.Errorf("create topic %q: %w", req.Name, err)
	}
	if resp.Err != nil {
		return fmt.Errorf("create topic %q: %w", req.Name, resp.Err)
	}
	return nil
}

// DeleteTopic deletes a topic.
func (r *Registry) DeleteTopic(ctx context.Context, cluster, topic string) error {
	adm, err := r.Admin(cluster)
	if err != nil {
		return err
	}
	resp, err := adm.DeleteTopic(ctx, topic)
	if err != nil {
		return fmt.Errorf("delete topic %q: %w", topic, err)
	}
	if resp.Err != nil {
		return fmt.Errorf("delete topic %q: %w", topic, resp.Err)
	}
	return nil
}

// DeleteRecordsRequest truncates the log of a topic up to (but not including)
// an offset per partition.
type DeleteRecordsRequest struct {
	// Partitions maps partition id -> low-watermark offset (records with
	// offset < this value will be deleted). Use -1 to delete everything up
	// to the current log-end offset.
	Partitions map[int32]int64 `json:"partitions"`
}

// DeleteRecordsResult is the per-partition outcome of a truncation.
type DeleteRecordsResult struct {
	Partition       int32  `json:"partition"`
	LowWatermark    int64  `json:"low_watermark"`
	RequestedOffset int64  `json:"requested_offset"`
	Error           string `json:"error,omitempty"`
}

// DeleteRecords truncates the topic log. Offsets of -1 are resolved to the
// current log-end offset before the request is issued.
func (r *Registry) DeleteRecords(ctx context.Context, cluster, topic string, req DeleteRecordsRequest) ([]DeleteRecordsResult, error) {
	if len(req.Partitions) == 0 {
		return nil, errors.New("at least one partition required")
	}
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}

	// Resolve "-1" (= delete everything) by looking up the log-end offset.
	needEnds := false
	for _, o := range req.Partitions {
		if o < 0 {
			needEnds = true
			break
		}
	}
	var ends kadm.ListedOffsets
	if needEnds {
		ends, err = adm.ListEndOffsets(ctx, topic)
		if err != nil {
			return nil, fmt.Errorf("list end offsets for topic %q: %w", topic, err)
		}
	}

	offsets := kadm.Offsets{}
	for p, o := range req.Partitions {
		off := o
		if off < 0 {
			if lo, ok := ends.Lookup(topic, p); ok && lo.Err == nil {
				off = lo.Offset
			} else {
				continue
			}
		}
		offsets.AddOffset(topic, p, off, -1)
	}
	if len(offsets) == 0 {
		return nil, errors.New("no resolvable partitions")
	}

	resp, err := adm.DeleteRecords(ctx, offsets)
	if err != nil {
		return nil, fmt.Errorf("delete records from topic %q: %w", topic, err)
	}

	parts := resp[topic]
	out := make([]DeleteRecordsResult, 0, len(req.Partitions))
	for p, requested := range req.Partitions {
		res := DeleteRecordsResult{Partition: p, RequestedOffset: requested, LowWatermark: -1}
		if pr, ok := parts[p]; ok {
			res.LowWatermark = pr.LowWatermark
			if pr.Err != nil {
				res.Error = pr.Err.Error()
			}
		} else {
			res.Error = "no response for partition"
		}
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Partition < out[j].Partition })
	return out, nil
}
