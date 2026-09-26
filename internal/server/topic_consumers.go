// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
)

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
