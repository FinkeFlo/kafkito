// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hamba/avro/v2"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// kfakeCluster is the cluster name every kfake-backed test registers.
const kfakeCluster = "kf"

// fixtureBaseTS is the timestamp of the first fixture record. Every record
// gets a distinct timestamp one second after the previous one, so the
// newest-first / oldest-first merge order of the fixture is fully determined.
const fixtureBaseTS int64 = 1_700_000_000_000

// kfakeEnv is an in-memory Kafka cluster plus a Registry pointed at it.
type kfakeEnv struct {
	reg     *Registry
	cl      *kgo.Client
	topic   string
	brokers []string
}

// newKfakeEnv starts a kfake cluster with one topic of the given partition
// count. mutate, when non-nil, adjusts the cluster config before the
// Registry is built (Schema Registry, masking rules, ...).
func newKfakeEnv(t *testing.T, topic string, partitions int32, mutate func(*config.ClusterConfig)) *kfakeEnv {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(partitions, topic))
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return kfakeEnvFor(t, c, topic, mutate)
}

// kfakeEnvFor builds the Registry and the producer client for an already
// started kfake cluster whose topic exists.
func kfakeEnvFor(t *testing.T, c *kfake.Cluster, topic string, mutate func(*config.ClusterConfig)) *kfakeEnv {
	t.Helper()
	cfg := config.ClusterConfig{Name: kfakeCluster, Brokers: c.ListenAddrs()}
	if mutate != nil {
		mutate(&cfg)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := NewRegistry([]config.ClusterConfig{cfg}, logger)
	t.Cleanup(reg.Close)

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(c.ListenAddrs()...),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
		kgo.ProducerBatchMaxBytes(4<<20),
	)
	require.NoError(t, err)
	t.Cleanup(cl.Close)
	return &kfakeEnv{reg: reg, cl: cl, topic: topic, brokers: c.ListenAddrs()}
}

// produce writes recs one by one (so each gets its own batch and keeps its
// explicit timestamp); ProduceSync fills in the assigned offsets.
func (e *kfakeEnv) produce(t *testing.T, recs ...*kgo.Record) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, r := range recs {
		r.Topic = e.topic
		res := e.cl.ProduceSync(ctx, r)
		require.NoError(t, res.FirstErr())
	}
}

// produceTransactional writes every record in its own committed transaction,
// so each record is followed by a commit marker offset.
func (e *kfakeEnv) produceTransactional(t *testing.T, recs ...*kgo.Record) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(e.brokers...),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
		kgo.TransactionalID("kafkito-test-"+e.topic),
	)
	require.NoError(t, err)
	defer cl.Close()
	for _, r := range recs {
		r.Topic = e.topic
		require.NoError(t, cl.BeginTransaction())
		require.NoError(t, cl.ProduceSync(ctx, r).FirstErr())
		require.NoError(t, cl.EndTransaction(ctx, kgo.TryCommit))
	}
}

// fixtureRecord is one entry of the shared multi-partition fixture.
type fixtureRecord struct {
	Seq       int
	Partition int32
	Offset    int64
	Timestamp int64
}

// fixturePattern spreads records over three partitions so that every
// partition's records interleave in time with the others: p0 gets half of
// all records, p1 a third and p2 a sixth.
var fixturePattern = []int32{0, 1, 0, 2, 0, 1}

// produceOrdersFixture writes n records following fixturePattern. Record i has
// key "k-<i>", a JSON value, a "seq" header and timestamp fixtureBaseTS+i s.
func (e *kfakeEnv) produceOrdersFixture(t *testing.T, n int) []fixtureRecord {
	t.Helper()
	out := make([]fixtureRecord, 0, n)
	next := map[int32]int64{}
	for i := range n {
		p := fixturePattern[i%len(fixturePattern)]
		ts := fixtureBaseTS + int64(i)*1000
		kind := "even"
		if i%2 == 1 {
			kind = "odd"
		}
		e.produce(t, &kgo.Record{
			Partition: p,
			Timestamp: time.UnixMilli(ts),
			Key:       fmt.Appendf(nil, "k-%d", i),
			Value:     fmt.Appendf(nil, `{"seq":%d,"kind":%q,"name":"rec-%d"}`, i, kind, i),
			Headers:   []kgo.RecordHeader{{Key: "seq", Value: fmt.Appendf(nil, "%d", i)}},
		})
		out = append(out, fixtureRecord{Seq: i, Partition: p, Offset: next[p], Timestamp: ts})
		next[p]++
	}
	return out
}

// seqOf extracts the fixture sequence number from a consumed message value.
func seqOf(t *testing.T, m Message) int {
	t.Helper()
	var v struct {
		Seq int `json:"seq"`
	}
	require.NoError(t, json.Unmarshal([]byte(m.Value), &v), "value %q", m.Value)
	return v.Seq
}

// seqs maps messages to their fixture sequence numbers.
func seqs(t *testing.T, msgs []Message) []int {
	t.Helper()
	out := make([]int, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, seqOf(t, m))
	}
	return out
}

// seqRange returns [from, to] inclusive, ascending (from<=to) or descending.
func seqRange(from, to int) []int {
	var out []int
	if from <= to {
		for i := from; i <= to; i++ {
			out = append(out, i)
		}
		return out
	}
	for i := from; i >= to; i-- {
		out = append(out, i)
	}
	return out
}

// avroUserSchema is the schema served by the fake Schema Registry.
const avroUserSchema = `{"type":"record","name":"User","fields":[{"name":"id","type":"long"},{"name":"name","type":"string"}]}`

// avroUserSchemaID is the id the fake Schema Registry serves avroUserSchema under.
const avroUserSchemaID = 7

// startFakeSchemaRegistry serves avroUserSchema under avroUserSchemaID.
func startFakeSchemaRegistry(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/schemas/ids/%d", avroUserSchemaID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema":     avroUserSchema,
			"schemaType": "AVRO",
			"subject":    "users-value",
			"version":    1,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// avroUserFrame encodes a User in the Confluent wire format.
func avroUserFrame(t *testing.T, id int64, name string) []byte {
	t.Helper()
	schema, err := avro.Parse(avroUserSchema)
	require.NoError(t, err)
	payload, err := avro.Marshal(schema, map[string]any{"id": id, "name": name})
	require.NoError(t, err)
	framed := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(framed[1:5], avroUserSchemaID)
	copy(framed[5:], payload)
	return framed
}
