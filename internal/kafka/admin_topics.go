// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
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
func (r *Topics) CreateTopic(ctx context.Context, cluster string, req CreateTopicRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return errors.New("topic name required")
	}
	cl, err := r.Client(cluster)
	if err != nil {
		return err
	}
	adm := kadm.NewClient(cl)
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
	waitForTopicMetadata(ctx, cl, req.Name)
	return nil
}

// topicVisibleTimeout bounds how long CreateTopic waits for brokers to list
// a new topic. The create itself has succeeded either way.
const topicVisibleTimeout = 5 * time.Second

// waitForTopicMetadata waits until a broker reports the topic without an
// error, the condition ListTopics uses. The UI refetches the topic list as
// soon as the create returns, and two things would hide the new topic from
// that list: brokers apply the controller's record asynchronously, and
// kadm serves topic lists from the client's metadata cache (MetadataMinAge).
// The direct, uncached request also stores the topic in that cache, so the
// next cached list includes it.
func waitForTopicMetadata(ctx context.Context, cl *kgo.Client, topic string) {
	ctx, cancel := context.WithTimeout(ctx, topicVisibleTimeout)
	defer cancel()
	req := kmsg.NewPtrMetadataRequest()
	rt := kmsg.NewMetadataRequestTopic()
	rt.Topic = kmsg.StringPtr(topic)
	req.Topics = append(req.Topics, rt)
	for {
		if resp, err := req.RequestWith(ctx, cl); err == nil {
			for _, t := range resp.Topics {
				if t.Topic != nil && *t.Topic == topic && t.ErrorCode == 0 {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// DeleteTopic deletes a topic.
func (r *Topics) DeleteTopic(ctx context.Context, cluster, topic string) error {
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
func (r *Topics) DeleteRecords(ctx context.Context, cluster, topic string, req DeleteRecordsRequest) ([]DeleteRecordsResult, error) {
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
