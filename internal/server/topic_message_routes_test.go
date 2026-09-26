package server

import (
	"bytes"
	"compress/gzip"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// startKfake starts an in-memory Kafka cluster with the given single-partition
// topics and returns its bootstrap address.
func startKfake(t *testing.T, topics ...string) string {
	t.Helper()
	opts := []kfake.Opt{kfake.NumBrokers(1)}
	if len(topics) > 0 {
		opts = append(opts, kfake.SeedTopics(1, topics...))
	}
	c, err := kfake.NewCluster(opts...)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c.ListenAddrs()[0]
}

// kfakeServer serves server.New against an in-memory Kafka cluster that is
// registered twice: as "kf" and as the production cluster "kfprod".
func kfakeServer(t *testing.T, topics ...string) http.Handler {
	t.Helper()
	addr := startKfake(t, topics...)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{
		{Name: "kf", Brokers: []string{addr}},
		{Name: "kfprod", Brokers: []string{addr}, IsProd: true},
	}, slog.Default())
	t.Cleanup(reg.Close)
	return New(Options{Version: "v-test", Logger: slog.Default(), Registry: reg, Config: config.Defaults()})
}

func gzipString(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(s))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.String()
}

// topicMessageCases walks the lifecycle of a topic on an in-memory cluster:
// the cases run in order and depend on each other's side effects.
func topicMessageCases(t *testing.T) []handlerCase {
	t.Helper()
	h := kfakeServer(t, "seeded", "copy-dest")
	const (
		base    = "/api/v1/clusters/kf/topics"
		prod    = "/api/v1/clusters/kfprod/topics"
		topic   = base + "/orders"
		jsonCT  = "application/json"
		confirm = "X-Kafkito-Confirm-Prod"
	)
	gz := map[string]string{"Content-Encoding": "gzip"}
	longPath := func(n int) string {
		return `{"mode":"contains","value":"hello","path":"` + strings.Repeat("a", n) + `"}`
	}
	cases := []requestCase{
		// listTopics
		{name: "topics", method: "GET", path: base, wantStatus: 200, wantBody: `"name":"seeded"`},
		{name: "topics unknown cluster", method: "GET", path: "/api/v1/clusters/nope/topics", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`},
		{name: "topics wrong method", method: "PUT", path: base, wantStatus: 405, notAnOperation: true},
		// createTopic
		{name: "create", method: "POST", path: base, contentType: jsonCT, body: `{"name":"orders","partitions":1,"replication_factor":1}`, wantStatus: 201, wantBody: `{"created":"orders"}`},
		// Broker-side rejections stay upstream errors, as before.
		{name: "create duplicate", method: "POST", path: base, contentType: jsonCT, body: `{"name":"orders"}`, wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		{name: "create without name", method: "POST", path: base, contentType: jsonCT, body: `{}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/name\": is required"}`},
		{name: "create unknown field", method: "POST", path: base, contentType: jsonCT, body: `{"name":"x","bogus":1}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "create partitions int32 overflow", method: "POST", path: base, contentType: jsonCT, body: `{"name":"x","partitions":2147483648}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "create without body", method: "POST", path: base, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "create wrong content type", method: "POST", path: base, contentType: "text/plain", body: `{"name":"x"}`, wantStatus: 400, wantBody: `request body: unsupported Content-Type`},
		// describeTopic
		{name: "describe", method: "GET", path: topic, wantStatus: 200, wantBody: `"name":"orders"`},
		{name: "describe unknown topic", method: "GET", path: base + "/missing", wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		// alterTopicConfigs
		{name: "configs", method: "PATCH", path: topic + "/configs", contentType: jsonCT, body: `{"set":{"retention.ms":"3600000"}}`, wantStatus: 200},
		{name: "configs non-string value", method: "PATCH", path: topic + "/configs", contentType: jsonCT, body: `{"set":{"retention.ms":1}}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "configs unknown field", method: "PATCH", path: topic + "/configs", contentType: jsonCT, body: `{"sett":{}}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		// produceMessage
		{name: "produce", method: "POST", path: topic + "/messages", contentType: jsonCT, body: `{"key":"k1","value":"hello world","headers":{"h":"v"}}`, wantStatus: 200, wantBody: `"offset":0`},
		{name: "produce gzip", method: "POST", path: topic + "/messages", contentType: jsonCT, header: gz, body: gzipString(t, `{"key":"k2","value":"{\"n\":2}"}`), wantStatus: 200, wantBody: `"offset":1`},
		{name: "produce base64", method: "POST", path: topic + "/messages", contentType: jsonCT, body: `{"value":"/w==","value_encoding":"base64"}`, wantStatus: 200, wantBody: `"offset":2`},
		{name: "produce invalid gzip", method: "POST", path: topic + "/messages", contentType: jsonCT, header: gz, body: `{"value":"x"}`, wantStatus: 400, wantBody: `"error":"invalid gzip body: `},
		// Only gzip is decoded (case-insensitively); any other encoding is
		// read as plain JSON, as before.
		{name: "produce identity encoding", method: "POST", path: topic + "/messages", contentType: jsonCT, header: map[string]string{"Content-Encoding": "identity"}, body: `{"value":"x"}`, wantStatus: 200, wantBody: `"offset":3`},
		{name: "produce upper-case gzip", method: "POST", path: topic + "/messages", contentType: jsonCT, header: map[string]string{"Content-Encoding": "GZIP"}, body: gzipString(t, `{"value":"y"}`), wantStatus: 200, wantBody: `"offset":4`},
		{name: "produce bad encoding enum", method: "POST", path: topic + "/messages", contentType: jsonCT, body: `{"value":"x","value_encoding":"hex"}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "produce invalid base64", method: "POST", path: topic + "/messages", contentType: jsonCT, body: `{"value":"!!","value_encoding":"base64"}`, wantStatus: 400},
		{name: "produce unknown field", method: "POST", path: topic + "/messages", contentType: jsonCT, body: `{"value":"x","bogus":1}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "produce prod without confirmation", method: "POST", path: prod + "/orders/messages", contentType: jsonCT, body: `{"value":"x"}`, wantStatus: 428, wantBody: `"code":"production_confirmation_required"`},
		{name: "produce prod confirmation not exactly true", method: "POST", path: prod + "/orders/messages", contentType: jsonCT, header: map[string]string{confirm: "TRUE"}, body: `{"value":"x"}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "produce prod confirmed", method: "POST", path: prod + "/orders/messages", contentType: jsonCT, header: map[string]string{confirm: "true"}, body: `{"value":"prod value"}`, wantStatus: 200, wantBody: `"offset":5`},
		// consumeMessages
		{name: "consume", method: "GET", path: topic + "/messages?from=start&limit=6", wantStatus: 200, wantBody: `"value":"hello world"`},
		{name: "consume limit 1", method: "GET", path: topic + "/messages?from=start&limit=1", wantStatus: 200, wantBody: `"next_cursor":`},
		{name: "consume limit above max is clamped", method: "GET", path: topic + "/messages?from=end&limit=100000", wantStatus: 200},
		{name: "consume limit 0", method: "GET", path: topic + "/messages?limit=0", wantStatus: 400, wantBody: `parameter \"limit\" in query`},
		{name: "consume limit not a number", method: "GET", path: topic + "/messages?limit=x", wantStatus: 400, wantBody: `parameter \"limit\" in query`},
		{name: "consume bad from", method: "GET", path: topic + "/messages?from=middle", wantStatus: 400, wantBody: `parameter \"from\" in query`},
		{name: "consume partition int32 overflow", method: "GET", path: topic + "/messages?partition=2147483648", wantStatus: 400, wantBody: `parameter \"partition\" in query`},
		{name: "consume timestamp 0", method: "GET", path: topic + "/messages?from=timestamp&from_ts_ms=0&limit=6", wantStatus: 200},
		{name: "consume timestamp -1", method: "GET", path: topic + "/messages?from=timestamp&from_ts_ms=-1", wantStatus: 400, wantBody: `parameter \"from_ts_ms\" in query`},
		{name: "consume malformed partition offsets", method: "GET", path: topic + "/messages?partition_offsets=zz", wantStatus: 400},
		{name: "consume malformed cursor", method: "GET", path: topic + "/messages?cursor=zz", wantStatus: 400},
		{name: "consume unknown topic", method: "GET", path: base + "/missing/messages", wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		// countMessages
		{name: "count", method: "GET", path: topic + "/messages/count", wantStatus: 200, wantBody: `"total_approx_count":6`},
		{name: "count bad partition", method: "GET", path: topic + "/messages/count?partition=x", wantStatus: 400, wantBody: `parameter \"partition\" in query`},
		// getMessageTimeline
		{name: "timeline", method: "GET", path: topic + "/messages/timeline?from_ts_ms=1&to_ts_ms=" + nowMs() + "&slot_ms=" + nowMs(), wantStatus: 200},
		{name: "timeline from 0", method: "GET", path: topic + "/messages/timeline?from_ts_ms=0&to_ts_ms=2&slot_ms=1", wantStatus: 400, wantBody: `parameter \"from_ts_ms\" in query`},
		{name: "timeline slot 0", method: "GET", path: topic + "/messages/timeline?from_ts_ms=1&to_ts_ms=2&slot_ms=0", wantStatus: 400, wantBody: `parameter \"slot_ms\" in query`},
		{name: "timeline missing slot", method: "GET", path: topic + "/messages/timeline?from_ts_ms=1&to_ts_ms=2", wantStatus: 400, wantBody: `parameter \"slot_ms\" in query: is required`},
		{name: "timeline too many slots", method: "GET", path: topic + "/messages/timeline?from_ts_ms=1&to_ts_ms=100000&slot_ms=1", wantStatus: 400},
		// downloadMessageRaw
		{name: "raw text", method: "GET", path: topic + "/messages/0/0/raw", wantStatus: 200, wantBody: "hello world", wantHeader: map[string]string{
			"Content-Type": "text/plain; charset=utf-8", "Content-Disposition": `attachment; filename="orders-p0-o0.txt"`, "Content-Length": "11",
		}},
		{name: "raw json", method: "GET", path: topic + "/messages/0/1/raw", wantStatus: 200, wantBody: `{"n":2}`, wantHeader: map[string]string{
			"Content-Type": "application/json", "Content-Disposition": `attachment; filename="orders-p0-o1.json"`,
		}},
		{name: "raw binary", method: "GET", path: topic + "/messages/0/2/raw", wantStatus: 200, wantHeader: map[string]string{
			"Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename="orders-p0-o2.bin"`, "Content-Length": "1",
		}},
		{name: "raw offset -1", method: "GET", path: topic + "/messages/0/-1/raw", wantStatus: 400, wantBody: `parameter \"offset\" in path`},
		{name: "raw partition not a number", method: "GET", path: topic + "/messages/x/0/raw", wantStatus: 400, wantBody: `parameter \"partition\" in path`},
		{name: "raw partition int32 overflow", method: "GET", path: topic + "/messages/2147483648/0/raw", wantStatus: 400, wantBody: `parameter \"partition\" in path`},
		{name: "raw offset past the end", method: "GET", path: topic + "/messages/0/99/raw", timeout: 5 * time.Second, wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
		// sampleMessages
		{name: "sample", method: "GET", path: topic + "/sample?n=100", wantStatus: 200, wantBody: `"messages":[`},
		{name: "sample bad n", method: "GET", path: topic + "/sample?n=x", wantStatus: 400, wantBody: `parameter \"n\" in query`},
		// searchMessages
		{name: "search hit", method: "POST", path: topic + "/messages/search", contentType: jsonCT, body: `{"mode":"contains","value":"hello","direction":"oldest_first"}`, wantStatus: 200, wantBody: `"value":"hello world"`},
		{name: "search without body", method: "POST", path: topic + "/messages/search", wantStatus: 200},
		{name: "search path at max length", method: "POST", path: topic + "/messages/search", contentType: jsonCT, body: longPath(2048), wantStatus: 200},
		{name: "search path above max length", method: "POST", path: topic + "/messages/search", contentType: jsonCT, body: longPath(2049), wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/path\": is out of range"}`},
		{name: "search bad mode", method: "POST", path: topic + "/messages/search", contentType: jsonCT, body: `{"mode":"sql"}`, wantStatus: 400, wantBody: `request body \"/mode\": must be one of the allowed values`},
		{name: "search invalid jsonpath", method: "POST", path: topic + "/messages/search", contentType: jsonCT, body: `{"mode":"jsonpath","path":"$[","op":"exists"}`, wantStatus: 400},
		{name: "search malformed JSON", method: "POST", path: topic + "/messages/search", contentType: jsonCT, body: `{"mode":`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: malformed"}`},
		// listTopicConsumers
		{name: "consumers", method: "GET", path: topic + "/consumers", wantStatus: 200},
		// copyMessages
		{name: "copy", method: "POST", path: topic + "/copy", contentType: jsonCT, body: `{"dest_cluster":"kf","dest_topic":"copy-dest","limit":6}`, wantStatus: 200, wantBody: `"done":true`, wantHeader: map[string]string{
			"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no", "Content-Length": "",
		}},
		{name: "copy reached the destination", method: "GET", path: base + "/copy-dest/messages?from=start&limit=6", wantStatus: 200, wantBody: `"value":"hello world"`},
		{name: "copy missing dest topic", method: "POST", path: topic + "/copy", contentType: jsonCT, body: `{"dest_cluster":"kf"}`, wantStatus: 400, wantBody: `request body \"/dest_topic\": is required`},
		{name: "copy unknown field", method: "POST", path: topic + "/copy", contentType: jsonCT, body: `{"dest_cluster":"kf","dest_topic":"copy-dest","bogus":1}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "copy to itself", method: "POST", path: topic + "/copy", contentType: jsonCT, body: `{"dest_cluster":"kf","dest_topic":"orders"}`, wantStatus: 400},
		{name: "copy unknown destination cluster", method: "POST", path: topic + "/copy", contentType: jsonCT, body: `{"dest_cluster":"nope","dest_topic":"copy-dest"}`, wantStatus: 400, wantBody: `{"error":"unknown dest_cluster: nope"}`},
		{name: "copy to prod without confirmation", method: "POST", path: topic + "/copy", contentType: jsonCT, body: `{"dest_cluster":"kfprod","dest_topic":"copy-dest"}`, wantStatus: 428, wantBody: `"code":"production_confirmation_required"`},
		// deleteRecords
		{name: "delete records prod without confirmation", method: "DELETE", path: prod + "/orders/records", contentType: jsonCT, body: `{"partitions":{"0":1}}`, wantStatus: 428, wantBody: `"code":"production_confirmation_required"`},
		{name: "delete records", method: "DELETE", path: topic + "/records", contentType: jsonCT, body: `{"partitions":{"0":1}}`, wantStatus: 200},
		{name: "delete records missing partitions", method: "DELETE", path: topic + "/records", contentType: jsonCT, body: `{}`, wantStatus: 400, wantBody: `request body \"/partitions\": is required`},
		{name: "delete records non-integer offset", method: "DELETE", path: topic + "/records", contentType: jsonCT, body: `{"partitions":{"0":"1"}}`, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		// deleteTopic
		{name: "delete prod without confirmation", method: "DELETE", path: prod + "/orders", wantStatus: 428, wantBody: `"code":"production_confirmation_required"`},
		{name: "delete prod confirmation false", method: "DELETE", path: prod + "/orders", header: map[string]string{confirm: "false"}, wantStatus: 400, wantBody: `"code":"invalid_request"`},
		{name: "delete", method: "DELETE", path: topic, wantStatus: 200},
		{name: "delete unknown topic", method: "DELETE", path: topic, wantStatus: 502, wantBody: `"code":"kafka_upstream"`},
	}
	out := make([]handlerCase, 0, len(cases))
	for _, c := range cases {
		out = append(out, handlerCase{h, c})
	}
	return out
}

// nowMs is an hour from now in epoch milliseconds.
func nowMs() string {
	return strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)
}
