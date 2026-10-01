// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// Small fakes of the store interfaces for handler tests that need no
// broker. Each fake embeds its interface, so a call the test did not
// expect panics (and the request fails with 500) instead of passing
// silently.

// fakeServer serves New with st in place of a registry.
func fakeServer(t *testing.T, st stores) http.Handler {
	t.Helper()
	return New(Options{
		Version: "v-test",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config:  config.Defaults(),
		stores:  &st,
	})
}

// runFakeCases sends each case to h and checks status, body and that the
// response conforms to the spec.
func runFakeCases(t *testing.T, h http.Handler, cases []requestCase) {
	t.Helper()
	router := contractRouter(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, rec := tc.do(t, h)
			assert.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantBody != "" {
				assert.Contains(t, rec.Body.String(), tc.wantBody)
			}
			assertResponseMatchesSpec(t, router, req, rec)
		})
	}
}

// fakeConfigs maps cluster names to their configs.
type fakeConfigs map[string]config.ClusterConfig

func (f fakeConfigs) ConfigFor(name string) (config.ClusterConfig, bool) {
	c, ok := f[name]
	return c, ok
}

type fakeGroups struct {
	groupStore
	groups       []kafkapkg.GroupInfo
	createGroup  func(req kafkapkg.CreateGroupRequest) (*kafkapkg.ResetOffsetsResult, error)
	resetOffsets func(group string, req kafkapkg.ResetOffsetsRequest) (*kafkapkg.ResetOffsetsResult, error)
}

func (f fakeGroups) ListGroups(context.Context, string) ([]kafkapkg.GroupInfo, error) {
	return f.groups, nil
}

func (f fakeGroups) CreateGroup(_ context.Context, _ string, req kafkapkg.CreateGroupRequest) (*kafkapkg.ResetOffsetsResult, error) {
	return f.createGroup(req)
}

func (f fakeGroups) ResetOffsets(_ context.Context, _, group string, req kafkapkg.ResetOffsetsRequest) (*kafkapkg.ResetOffsetsResult, error) {
	return f.resetOffsets(group, req)
}

type fakeACLs struct {
	aclStore
	createACL func(spec kafkapkg.ACLSpec) error
	deleteACL func(spec kafkapkg.ACLSpec) (int, error)
}

func (f fakeACLs) CreateACL(_ context.Context, _ string, spec kafkapkg.ACLSpec) error {
	return f.createACL(spec)
}

func (f fakeACLs) DeleteACL(_ context.Context, _ string, spec kafkapkg.ACLSpec) (int, error) {
	return f.deleteACL(spec)
}

// fakeSCRAM implements every scramStore method, so it embeds none.
type fakeSCRAM struct {
	users  []kafkapkg.SCRAMUser
	upsert func(user, mechanism, password string, iterations int32) error
	delete func(user, mechanism string) error
}

func (f fakeSCRAM) ListSCRAMUsers(context.Context, string) ([]kafkapkg.SCRAMUser, error) {
	return f.users, nil
}

func (f fakeSCRAM) UpsertSCRAMUser(_ context.Context, _, user, mechanism, password string, iterations int32) error {
	return f.upsert(user, mechanism, password, iterations)
}

func (f fakeSCRAM) DeleteSCRAMUser(_ context.Context, _, user, mechanism string) error {
	return f.delete(user, mechanism)
}

// fakeSchemas hands out client, or fails with err.
type fakeSchemas struct {
	client schemaClient
	err    error
}

func (f fakeSchemas) SchemaRegistry(string) (schemaClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

// fakeSchemaClient answers every call with err, or with its canned data.
type fakeSchemaClient struct {
	err      error
	subjects []kafkapkg.Subject
	// deleted records the arguments of DeleteSubject.
	deleted func(subject string, permanent bool)
}

func (f fakeSchemaClient) ListSubjectsWithVersions(context.Context) ([]kafkapkg.Subject, error) {
	return f.subjects, f.err
}

func (f fakeSchemaClient) ListVersions(context.Context, string) ([]int, error) {
	return []int{1}, f.err
}

func (f fakeSchemaClient) GetVersion(_ context.Context, subject, _ string) (*kafkapkg.SchemaVersion, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &kafkapkg.SchemaVersion{Subject: subject, ID: 1, Version: 1, SchemaType: "AVRO", Schema: `"string"`}, nil
}

func (f fakeSchemaClient) RegisterSchema(context.Context, string, kafkapkg.RegisterSchemaRequest) (*kafkapkg.RegisterSchemaResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &kafkapkg.RegisterSchemaResponse{ID: 1}, nil
}

func (f fakeSchemaClient) DeleteSubject(_ context.Context, subject string, permanent bool) ([]int, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.deleted != nil {
		f.deleted(subject, permanent)
	}
	return []int{1}, nil
}

type fakeMessages struct {
	messageStore
	raw      func(topic string, partition int32, offset int64, opts kafkapkg.RawValueOptions) (*kafkapkg.RawMessageValue, error)
	consume  func(opts kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error)
	count    func(opts kafkapkg.CountMessagesOptions) (*kafkapkg.MessageCountResult, error)
	timeline func(opts kafkapkg.MessageTimelineOptions) (*kafkapkg.MessageTimelineResult, error)
	search   func(opts kafkapkg.SearchOptions) (*kafkapkg.SearchResult, error)
}

func (f fakeMessages) SearchMessages(_ context.Context, _, _ string, opts kafkapkg.SearchOptions) (*kafkapkg.SearchResult, error) {
	return f.search(opts)
}

func (f fakeMessages) FetchRawMessageValue(_ context.Context, _, topic string, partition int32, offset int64, opts kafkapkg.RawValueOptions) (*kafkapkg.RawMessageValue, error) {
	return f.raw(topic, partition, offset, opts)
}

func (f fakeMessages) ConsumeMessages(_ context.Context, _, _ string, opts kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error) {
	return f.consume(opts)
}

func (f fakeMessages) CountMessages(_ context.Context, _, _ string, opts kafkapkg.CountMessagesOptions) (*kafkapkg.MessageCountResult, error) {
	return f.count(opts)
}

func (f fakeMessages) MessageTimeline(_ context.Context, _, _ string, opts kafkapkg.MessageTimelineOptions) (*kafkapkg.MessageTimelineResult, error) {
	return f.timeline(opts)
}

// fakeTopics answers the topic list and the consumers of any topic.
type fakeTopics struct {
	topicStore
	topics    []kafkapkg.TopicInfo
	consumers []kafkapkg.TopicConsumer
	// writeErr, when set, fails CreateTopic, AlterTopicConfigs and
	// DeleteRecords.
	writeErr error
}

func (f fakeTopics) CreateTopic(context.Context, string, kafkapkg.CreateTopicRequest) error {
	return f.writeErr
}

func (f fakeTopics) AlterTopicConfigs(context.Context, string, string, kafkapkg.AlterTopicConfigsRequest) ([]kafkapkg.AlterTopicConfigsResult, error) {
	return nil, f.writeErr
}

func (f fakeTopics) DeleteRecords(context.Context, string, string, kafkapkg.DeleteRecordsRequest) ([]kafkapkg.DeleteRecordsResult, error) {
	return nil, f.writeErr
}

func (f fakeTopics) ListTopics(context.Context, string) ([]kafkapkg.TopicInfo, error) {
	return f.topics, nil
}

func (f fakeTopics) ListTopicConsumers(context.Context, string, string) ([]kafkapkg.TopicConsumer, error) {
	return f.consumers, nil
}
