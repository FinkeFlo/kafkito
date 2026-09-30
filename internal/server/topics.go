// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// maxJSONBodyBytes caps the JSON bodies of the topic admin endpoints
// (create topic, alter configs, delete records) and the bodies the RBAC
// middleware reads to resolve a resource name.
const maxJSONBodyBytes = 1 << 20

// ListTopics returns the topics of the named cluster.
//
// Budget is 15s (matches test-connection). Private/browser-stored clusters
// trigger an inline metrics probe inside Registry.ListTopics that can take
// 5–12s on a cold load and is cached for ~30s afterwards.
func (s *apiServer) ListTopics(ctx context.Context, req gen.ListTopicsRequestObject) (gen.ListTopicsResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	topics, err := s.topics.ListTopics(ctx, req.Cluster)
	if err != nil {
		return nil, clusterError(req.Cluster, "list topics", err)
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
	if user, ok := s.rbacListSubject(ctx, req.Cluster); ok {
		topics = filterTopicsByRBAC(topics, s.policy, user, req.Cluster)
	}
	return gen.ListTopics200JSONResponse{Cluster: config.PublicClusterName(req.Cluster), Topics: topics}, nil
}

// CreateTopic creates a topic on the cluster.
func (s *apiServer) CreateTopic(ctx context.Context, req gen.CreateTopicRequestObject) (gen.CreateTopicResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.topics.CreateTopic(ctx, req.Cluster, *req.Body); err != nil {
		if blocked := blockedAddressError("create topic", err); blocked != nil {
			return nil, blocked
		}
		if msg := err.Error(); !errors.Is(err, kafkapkg.ErrUnknownCluster) && strings.Contains(msg, "topic name required") {
			return nil, badRequest("kafka: " + msg)
		}
		return nil, clusterError(req.Cluster, "create topic", err)
	}
	return gen.CreateTopic201JSONResponse{Created: req.Body.Name}, nil
}

// DescribeTopic returns full detail (partitions, offsets, configs) for a topic.
func (s *apiServer) DescribeTopic(ctx context.Context, req gen.DescribeTopicRequestObject) (gen.DescribeTopicResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	detail, err := s.topics.DescribeTopic(ctx, req.Cluster, req.Topic)
	if err != nil {
		return nil, clusterError(req.Cluster, "describe topic", err)
	}
	sort.Slice(detail.Partitions, func(i, j int) bool {
		return detail.Partitions[i].Partition < detail.Partitions[j].Partition
	})
	sort.Slice(detail.Configs, func(i, j int) bool {
		return detail.Configs[i].Name < detail.Configs[j].Name
	})
	return gen.DescribeTopic200JSONResponse{Cluster: config.PublicClusterName(req.Cluster), Topic: *detail}, nil
}

// DeleteTopic removes a topic.
func (s *apiServer) DeleteTopic(ctx context.Context, req gen.DeleteTopicRequestObject) (gen.DeleteTopicResponseObject, error) {
	if err := prodConfirmationError(s.configs, req.Cluster, httpRequestFromContext(ctx)); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.topics.DeleteTopic(ctx, req.Cluster, req.Topic); err != nil {
		return nil, clusterError(req.Cluster, "delete topic", err)
	}
	return gen.DeleteTopic200JSONResponse{Deleted: req.Topic}, nil
}

// ListTopicConsumers returns the consumer groups currently reading from the
// given topic, filtered by the caller's RBAC group:view permission. Bounded
// by a 5s upstream timeout.
func (s *apiServer) ListTopicConsumers(ctx context.Context, req gen.ListTopicConsumersRequestObject) (gen.ListTopicConsumersResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	consumers, err := s.topics.ListTopicConsumers(ctx, req.Cluster, req.Topic)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, &apiError{
				Status:  http.StatusGatewayTimeout,
				Code:    "topic_consumers_timeout",
				Message: "timeout while listing consumers for topic " + req.Topic,
				Err:     err,
			}
		case errors.Is(err, kafkapkg.ErrTopicNotFound):
			return nil, &apiError{Status: http.StatusNotFound, Message: "unknown topic: " + req.Topic, Err: err}
		}
		return nil, clusterError(req.Cluster, "list consumers for topic "+req.Topic, err)
	}

	if user, ok := s.rbacListSubject(ctx, req.Cluster); ok {
		consumers = filterTopicConsumersByRBAC(consumers, s.policy, user, req.Cluster)
	}
	return gen.ListTopicConsumers200JSONResponse{Cluster: config.PublicClusterName(req.Cluster), Topic: req.Topic, Consumers: consumers}, nil
}

// AlterTopicConfigs applies incremental config changes to a topic.
func (s *apiServer) AlterTopicConfigs(ctx context.Context, req gen.AlterTopicConfigsRequestObject) (gen.AlterTopicConfigsResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := s.topics.AlterTopicConfigs(ctx, req.Cluster, req.Topic, *req.Body)
	if err != nil {
		if blocked := blockedAddressError("alter topic configs", err); blocked != nil {
			return nil, blocked
		}
		if msg := err.Error(); !errors.Is(err, kafkapkg.ErrUnknownCluster) && (strings.Contains(msg, "required") || strings.Contains(msg, "no changes")) {
			return nil, badRequest("kafka: " + msg)
		}
		return nil, clusterError(req.Cluster, "alter topic configs", err)
	}
	return gen.AlterTopicConfigs200JSONResponse{Results: res}, nil
}

// DeleteRecords truncates the topic log per partition.
func (s *apiServer) DeleteRecords(ctx context.Context, req gen.DeleteRecordsRequestObject) (gen.DeleteRecordsResponseObject, error) {
	if err := prodConfirmationError(s.configs, req.Cluster, httpRequestFromContext(ctx)); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := s.topics.DeleteRecords(ctx, req.Cluster, req.Topic, *req.Body)
	if err != nil {
		if blocked := blockedAddressError("delete records", err); blocked != nil {
			return nil, blocked
		}
		if msg := err.Error(); !errors.Is(err, kafkapkg.ErrUnknownCluster) && (strings.Contains(msg, "required") || strings.Contains(msg, "no resolvable")) {
			return nil, badRequest("kafka: " + msg)
		}
		return nil, clusterError(req.Cluster, "delete records", err)
	}
	return gen.DeleteRecords200JSONResponse{Results: res}, nil
}

// filterTopicsByRBAC removes topics from the list the user is not allowed to
// view. When the user has '*' access, the list is returned unchanged.
func filterTopicsByRBAC(topics []kafkapkg.TopicInfo, policy *rbac.Policy, user, cluster string) []kafkapkg.TopicInfo {
	globs, all := policy.AllowedResourceNames(user, cluster, "topic", "view")
	if all {
		return topics
	}
	if len(globs) == 0 {
		return []kafkapkg.TopicInfo{}
	}
	out := make([]kafkapkg.TopicInfo, 0, len(topics))
	for _, t := range topics {
		for _, glob := range globs {
			if rbac.MatchName(glob, t.Name) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}
