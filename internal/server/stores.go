// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

// The interfaces below are what the handlers and middleware need from the
// kafka layer, each limited to the methods its consumers call. Handler
// tests can substitute small fakes for them instead of a broker.

// clusterConfigs looks up the config of a static or ad-hoc cluster.
type clusterConfigs interface {
	ConfigFor(name string) (config.ClusterConfig, bool)
}

// adhocClusters registers a private cluster and returns its internal name.
type adhocClusters interface {
	UseAdhoc(cfg config.ClusterConfig) (string, error)
}

// clusterStore backs the cluster, broker, capability and readiness handlers.
type clusterStore interface {
	adhocClusters
	Names() []string
	Describe(ctx context.Context, probeTimeout time.Duration) []kafkapkg.ClusterInfo
	Client(name string) (*kgo.Client, error)
	Ping(ctx context.Context, name string) error
	ProbeBrokers(ctx context.Context, name string) (issues []kafkapkg.BrokerIssue, skipped int, err error)
	Capabilities(ctx context.Context, cluster string) (*kafkapkg.Capabilities, error)
	RefreshCapabilities(cluster string)
	ListBrokers(ctx context.Context, cluster string) ([]kafkapkg.BrokerInfo, error)
}

// topicStore backs the topic handlers.
type topicStore interface {
	ListTopics(ctx context.Context, cluster string) ([]kafkapkg.TopicInfo, error)
	CreateTopic(ctx context.Context, cluster string, req kafkapkg.CreateTopicRequest) error
	DescribeTopic(ctx context.Context, cluster, topic string) (*kafkapkg.TopicDetail, error)
	DeleteTopic(ctx context.Context, cluster, topic string) error
	ListTopicConsumers(ctx context.Context, cluster, topic string) ([]kafkapkg.TopicConsumer, error)
	AlterTopicConfigs(ctx context.Context, cluster, topic string, req kafkapkg.AlterTopicConfigsRequest) ([]kafkapkg.AlterTopicConfigsResult, error)
	DeleteRecords(ctx context.Context, cluster, topic string, req kafkapkg.DeleteRecordsRequest) ([]kafkapkg.DeleteRecordsResult, error)
}

// groupStore backs the consumer group handlers.
type groupStore interface {
	ListGroups(ctx context.Context, cluster string) ([]kafkapkg.GroupInfo, error)
	CreateGroup(ctx context.Context, cluster string, req kafkapkg.CreateGroupRequest) (*kafkapkg.ResetOffsetsResult, error)
	DescribeGroup(ctx context.Context, cluster, group string) (*kafkapkg.GroupDetail, error)
	DeleteGroup(ctx context.Context, cluster, group string) error
	ResetOffsets(ctx context.Context, cluster, group string, req kafkapkg.ResetOffsetsRequest) (*kafkapkg.ResetOffsetsResult, error)
}

// messageStore backs the message read and produce handlers.
type messageStore interface {
	ConsumeMessages(ctx context.Context, cluster, topic string, opts kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error)
	FetchRawMessageValue(ctx context.Context, cluster, topic string, partition int32, offset int64, opts kafkapkg.RawValueOptions) (*kafkapkg.RawMessageValue, error)
	CountMessages(ctx context.Context, cluster, topic string, opts kafkapkg.CountMessagesOptions) (*kafkapkg.MessageCountResult, error)
	MessageTimeline(ctx context.Context, cluster, topic string, opts kafkapkg.MessageTimelineOptions) (*kafkapkg.MessageTimelineResult, error)
	SearchMessages(ctx context.Context, cluster, topic string, opts kafkapkg.SearchOptions) (*kafkapkg.SearchResult, error)
	Produce(ctx context.Context, cluster, topic string, req kafkapkg.ProduceRequest) (*kafkapkg.ProduceResult, error)
}

// aclStore backs the ACL handlers.
type aclStore interface {
	ListACLs(ctx context.Context, cluster string) ([]kafkapkg.ACLEntry, error)
	CreateACL(ctx context.Context, cluster string, spec kafkapkg.ACLSpec) error
	DeleteACL(ctx context.Context, cluster string, spec kafkapkg.ACLSpec) (int, error)
}

// scramStore backs the SCRAM user handlers.
type scramStore interface {
	ListSCRAMUsers(ctx context.Context, cluster string) ([]kafkapkg.SCRAMUser, error)
	UpsertSCRAMUser(ctx context.Context, cluster, user, mechanism, password string, iterations int32) error
	DeleteSCRAMUser(ctx context.Context, cluster, user, mechanism string) error
}

// schemaClient is the part of the Schema Registry client the schema
// handlers call.
type schemaClient interface {
	ListSubjectsWithVersions(ctx context.Context) ([]kafkapkg.Subject, error)
	ListVersions(ctx context.Context, subject string) ([]int, error)
	GetVersion(ctx context.Context, subject, version string) (*kafkapkg.SchemaVersion, error)
	RegisterSchema(ctx context.Context, subject string, req kafkapkg.RegisterSchemaRequest) (*kafkapkg.RegisterSchemaResponse, error)
	DeleteSubject(ctx context.Context, subject string, permanent bool) ([]int, error)
}

// schemaStore returns the Schema Registry client of a cluster.
type schemaStore interface {
	SchemaRegistry(cluster string) (schemaClient, error)
}

// connectionSchemas adapts kafka.Connections, whose SchemaRegistry returns
// the concrete client, to schemaStore.
type connectionSchemas struct {
	conns *kafkapkg.Connections
}

func (c connectionSchemas) SchemaRegistry(cluster string) (schemaClient, error) {
	sr, err := c.conns.SchemaRegistry(cluster)
	if err != nil {
		return nil, err
	}
	return sr, nil
}

// stores are the kafka-backed dependencies of apiServer.
type stores struct {
	configs  clusterConfigs
	clusters clusterStore
	topics   topicStore
	groups   groupStore
	messages messageStore
	schemas  schemaStore
	acls     aclStore
	scram    scramStore
	copyReg  copyRegistry
}

// registryStores wires the stores from the registry's services. A nil
// registry (no kafka configured) leaves every store nil.
func registryStores(reg *kafkapkg.Registry) stores {
	if reg == nil {
		return stores{}
	}
	return stores{
		configs:  reg.Connections,
		clusters: reg.Clusters,
		topics:   reg.Topics,
		groups:   reg.Groups,
		messages: reg.Messages,
		schemas:  connectionSchemas{conns: reg.Connections},
		acls:     reg.Security,
		scram:    reg.Security,
		copyReg:  reg,
	}
}
