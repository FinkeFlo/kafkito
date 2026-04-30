// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"sort"

	"github.com/twmb/franz-go/pkg/kadm"
)

// GroupInfo is a compact summary of a consumer group for list pages.
type GroupInfo struct {
	GroupID       string `json:"group_id"`
	State         string `json:"state"`
	ProtocolType  string `json:"protocol_type"`
	Protocol      string `json:"protocol,omitempty"`
	CoordinatorID int32  `json:"coordinator_id"`
	Members       int    `json:"members"`
	Topics        int    `json:"topics,omitempty"`
	Lag           int64  `json:"lag"`
	LagKnown      bool   `json:"lag_known"`
	Error         string `json:"error,omitempty"`
}

// GroupMember is one member in a consumer group.
type GroupMember struct {
	MemberID    string             `json:"member_id"`
	InstanceID  string             `json:"instance_id,omitempty"`
	ClientID    string             `json:"client_id"`
	ClientHost  string             `json:"client_host"`
	Assignments []MemberAssignment `json:"assignments"`
}

// MemberAssignment lists the partitions a member is responsible for in a single topic.
type MemberAssignment struct {
	Topic      string  `json:"topic"`
	Partitions []int32 `json:"partitions"`
}

// GroupOffset is one committed offset for a group on a (topic,partition).
type GroupOffset struct {
	Topic      string `json:"topic"`
	Partition  int32  `json:"partition"`
	Offset     int64  `json:"offset"`
	LogEnd     int64  `json:"log_end"`
	Lag        int64  `json:"lag"`
	Metadata   string `json:"metadata,omitempty"`
	AssignedTo string `json:"assigned_to,omitempty"`
}

// GroupDetail is the full describe-view of a consumer group.
type GroupDetail struct {
	GroupInfo
	Members []GroupMember `json:"members"`
	Offsets []GroupOffset `json:"offsets"`
}

// ListGroups returns the groups on the cluster, sorted by GroupID.
// Lag is computed per-group; a group with a fetch-offsets error returns LagKnown=false.
func (r *Registry) ListGroups(ctx context.Context, cluster string) ([]GroupInfo, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	listed, err := adm.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups on cluster %q: %w", cluster, err)
	}
	names := listed.Groups()
	out := make([]GroupInfo, 0, len(names))
	if len(names) == 0 {
		return out, nil
	}

	described, err := adm.DescribeGroups(ctx, names...)
	if err != nil {
		return nil, fmt.Errorf("describe %d groups on cluster %q: %w", len(names), cluster, err)
	}

	for _, name := range names {
		g := GroupInfo{GroupID: name}
		if l, ok := listed[name]; ok {
			g.State = l.State
			g.ProtocolType = l.ProtocolType
			g.CoordinatorID = l.Coordinator
		}
		if d, ok := described[name]; ok {
			if d.Err != nil {
				g.Error = d.Err.Error()
			} else {
				if g.State == "" {
					g.State = d.State
				}
				g.Protocol = d.Protocol
				g.ProtocolType = d.ProtocolType
				g.CoordinatorID = d.Coordinator.NodeID
				g.Members = len(d.Members)
				topicSet := map[string]struct{}{}
				for _, m := range d.Members {
					if ca, ok := m.Assigned.AsConsumer(); ok {
						for _, t := range ca.Topics {
							topicSet[t.Topic] = struct{}{}
						}
					}
				}
				g.Topics = len(topicSet)
			}
		}

		// Fetch committed offsets + lag. Unauthorized users get an error here;
		// in that case we still return the group summary without lag.
		lag, lagKnown, lerr := groupLag(ctx, adm, name)
		if lerr != nil {
			// Preserve any previously set error but prefer the describe error.
			if g.Error == "" {
				g.Error = classifyErr(lerr)
			}
		} else {
			g.Lag = lag
			g.LagKnown = lagKnown
		}

		out = append(out, g)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].GroupID < out[j].GroupID })
	return out, nil
}

// DescribeGroup returns the full detail of a single consumer group.
func (r *Registry) DescribeGroup(ctx context.Context, cluster, group string) (*GroupDetail, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}

	described, err := adm.DescribeGroups(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("describe group %q on cluster %q: %w", group, cluster, err)
	}
	d, ok := described[group]
	if !ok {
		return nil, fmt.Errorf("describe group %q on cluster %q: group not returned by broker", group, cluster)
	}
	if d.Err != nil {
		return nil, fmt.Errorf("describe group %q on cluster %q: %w", group, cluster, d.Err)
	}

	info := GroupInfo{
		GroupID:       group,
		State:         d.State,
		ProtocolType:  d.ProtocolType,
		Protocol:      d.Protocol,
		CoordinatorID: d.Coordinator.NodeID,
		Members:       len(d.Members),
	}

	// Walk members + build partition-to-member lookup.
	assignedTo := map[string]string{}
	members := make([]GroupMember, 0, len(d.Members))
	topicSet := map[string]struct{}{}
	for _, m := range d.Members {
		gm := GroupMember{
			MemberID:   m.MemberID,
			ClientID:   m.ClientID,
			ClientHost: m.ClientHost,
		}
		if m.InstanceID != nil {
			gm.InstanceID = *m.InstanceID
		}
		if ca, ok := m.Assigned.AsConsumer(); ok {
			for _, t := range ca.Topics {
				topicSet[t.Topic] = struct{}{}
				parts := append([]int32(nil), t.Partitions...)
				sort.Slice(parts, func(i, j int) bool { return parts[i] < parts[j] })
				gm.Assignments = append(gm.Assignments, MemberAssignment{
					Topic:      t.Topic,
					Partitions: parts,
				})
				for _, p := range parts {
					assignedTo[tpKey(t.Topic, p)] = m.ClientID + "@" + m.ClientHost
				}
			}
		}
		sort.Slice(gm.Assignments, func(i, j int) bool { return gm.Assignments[i].Topic < gm.Assignments[j].Topic })
		members = append(members, gm)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].MemberID < members[j].MemberID })
	info.Topics = len(topicSet)

	// Committed offsets.
	fetched, ferr := adm.FetchOffsets(ctx, group)
	offsets := []GroupOffset{}
	var topicsToLookup []string
	seen := map[string]struct{}{}
	if ferr == nil {
		for topic := range fetched {
			if _, ok := seen[topic]; !ok {
				seen[topic] = struct{}{}
				topicsToLookup = append(topicsToLookup, topic)
			}
		}
		// Add assigned topics that might not yet have committed offsets.
		for t := range topicSet {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				topicsToLookup = append(topicsToLookup, t)
			}
		}
	}

	var ends kadm.ListedOffsets
	if len(topicsToLookup) > 0 {
		ends, _ = adm.ListEndOffsets(ctx, topicsToLookup...)
	}

	if ferr == nil {
		for topic, parts := range fetched {
			for p, oe := range parts {
				if oe.Err != nil {
					continue
				}
				logEnd := int64(-1)
				if eo, ok := ends.Lookup(topic, p); ok {
					logEnd = eo.Offset
				}
				lag := int64(-1)
				if logEnd >= 0 && oe.At >= 0 {
					l := logEnd - oe.At
					if l < 0 {
						l = 0
					}
					lag = l
				}
				offsets = append(offsets, GroupOffset{
					Topic:      topic,
					Partition:  p,
					Offset:     oe.At,
					LogEnd:     logEnd,
					Lag:        lag,
					Metadata:   oe.Metadata,
					AssignedTo: assignedTo[tpKey(topic, p)],
				})
			}
		}
	}
	sort.Slice(offsets, func(i, j int) bool {
		if offsets[i].Topic != offsets[j].Topic {
			return offsets[i].Topic < offsets[j].Topic
		}
		return offsets[i].Partition < offsets[j].Partition
	})

	// Total lag for the summary.
	var total int64
	known := true
	for _, o := range offsets {
		if o.Lag < 0 {
			known = false
			break
		}
		total += o.Lag
	}
	info.Lag = total
	info.LagKnown = known && ferr == nil

	return &GroupDetail{
		GroupInfo: info,
		Members:   members,
		Offsets:   offsets,
	}, nil
}

// groupLag returns the aggregate lag across all committed (topic,partition)s of a group.
// Returns (lag, lagKnown, err). lagKnown=false if any partition's log-end is unknown.
func groupLag(ctx context.Context, adm *kadm.Client, group string) (int64, bool, error) {
	fetched, err := adm.FetchOffsets(ctx, group)
	if err != nil {
		return 0, false, err
	}
	if ferr := fetched.Error(); ferr != nil {
		return 0, false, ferr
	}
	if len(fetched) == 0 {
		return 0, true, nil
	}
	topics := make([]string, 0, len(fetched))
	for t := range fetched {
		topics = append(topics, t)
	}
	ends, eerr := adm.ListEndOffsets(ctx, topics...)
	if eerr != nil {
		return 0, false, eerr
	}
	var total int64
	known := true
	for topic, parts := range fetched {
		for p, oe := range parts {
			if oe.Err != nil || oe.At < 0 {
				known = false
				continue
			}
			eo, ok := ends.Lookup(topic, p)
			if !ok || eo.Offset < 0 {
				known = false
				continue
			}
			l := eo.Offset - oe.At
			if l < 0 {
				l = 0
			}
			total += l
		}
	}
	return total, known, nil
}

func tpKey(topic string, partition int32) string {
	return fmt.Sprintf("%s/%d", topic, partition)
}
