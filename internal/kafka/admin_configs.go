// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"strings"

	"github.com/twmb/franz-go/pkg/kadm"
)

// --- Alter topic configs ------------------------------------------------------

// AlterTopicConfigsRequest incrementally alters configuration entries on a topic.
//   - Set:    overrides/sets the key (string value).
//   - Delete: resets the key to broker default.
type AlterTopicConfigsRequest struct {
	Set    map[string]string `json:"set,omitempty"`
	Delete []string          `json:"delete,omitempty"`
}

// AlterTopicConfigsResult is the per-key outcome.
type AlterTopicConfigsResult struct {
	Name  string `json:"name"`
	Op    string `json:"op"` // set | delete
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// AlterTopicConfigs applies an incremental alter to the topic.
func (r *Registry) AlterTopicConfigs(ctx context.Context, cluster, topic string, req AlterTopicConfigsRequest) ([]AlterTopicConfigsResult, error) {
	if strings.TrimSpace(topic) == "" {
		return nil, fmt.Errorf("topic name required")
	}
	if len(req.Set) == 0 && len(req.Delete) == 0 {
		return nil, fmt.Errorf("no changes specified")
	}
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}

	ops := make([]kadm.AlterConfig, 0, len(req.Set)+len(req.Delete))
	results := make([]AlterTopicConfigsResult, 0, len(req.Set)+len(req.Delete))
	for k, v := range req.Set {
		val := v
		ops = append(ops, kadm.AlterConfig{Op: kadm.SetConfig, Name: k, Value: &val})
		results = append(results, AlterTopicConfigsResult{Name: k, Op: "set", Value: v})
	}
	for _, k := range req.Delete {
		ops = append(ops, kadm.AlterConfig{Op: kadm.DeleteConfig, Name: k})
		results = append(results, AlterTopicConfigsResult{Name: k, Op: "delete"})
	}

	responses, err := adm.AlterTopicConfigs(ctx, ops, topic)
	if err != nil {
		return nil, fmt.Errorf("alter configs for topic %q: %w", topic, err)
	}
	// Broker returns one response per topic (not per key). If it failed, mark all keys with the error.
	var topicErr string
	for _, resp := range responses {
		if resp.Name == topic && resp.Err != nil {
			topicErr = resp.Err.Error()
			if resp.ErrMessage != "" {
				topicErr = topicErr + ": " + resp.ErrMessage
			}
		}
	}
	if topicErr != "" {
		for i := range results {
			results[i].Error = topicErr
		}
	}
	return results, nil
}
