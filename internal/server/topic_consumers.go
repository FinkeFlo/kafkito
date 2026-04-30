// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
	"github.com/FinkeFlo/kafkito/pkg/rbac"
	"github.com/go-chi/chi/v5"
)

// listTopicConsumers returns the consumer groups currently reading from the
// given topic. Bounded by a 5s upstream timeout per usability plan §2.
func (a *clusterAPI) listTopicConsumers(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	if a.policy != nil && a.policy.Enabled() {
		user := r.Header.Get(a.policy.Header())
		// Topic-level gate: the user must be allowed to view the topic itself.
		if !a.policy.Allow(user, cluster, "topic", topic, "view") {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "forbidden",
				"code":  "rbac_denied",
			})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	consumers, err := a.reg.ListTopicConsumers(ctx, cluster, topic)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{
				"error": "timeout while listing consumers for topic " + topic,
				"code":  "topic_consumers_timeout",
			})
		case errors.Is(err, kafkapkg.ErrUnknownCluster):
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "unknown cluster: " + cluster,
			})
		case errors.Is(err, kafkapkg.ErrTopicNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "unknown topic: " + topic,
			})
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "kafka: " + err.Error(),
			})
		}
		return
	}

	if a.policy != nil && a.policy.Enabled() {
		user := r.Header.Get(a.policy.Header())
		consumers = filterTopicConsumersByRBAC(consumers, a.policy, user, cluster)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":   cluster,
		"topic":     topic,
		"consumers": consumers,
	})
}

// filterTopicConsumersByRBAC removes consumers whose group the user is not
// allowed to view.
func filterTopicConsumersByRBAC(in []kafkapkg.TopicConsumer, policy *rbac.Policy, user, cluster string) []kafkapkg.TopicConsumer {
	globs, all := policy.AllowedResourceNames(user, cluster, "group", "view")
	if all {
		return in
	}
	if len(globs) == 0 {
		return []kafkapkg.TopicConsumer{}
	}
	out := make([]kafkapkg.TopicConsumer, 0, len(in))
	for _, c := range in {
		for _, glob := range globs {
			if rbac.MatchName(glob, c.GroupID) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
