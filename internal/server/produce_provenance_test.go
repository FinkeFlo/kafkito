// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// provenanceIdentityHeader is the HTTP header the provenance tests resolve
// the RBAC subject from; distinct from the X-Kafkito-User record header so a
// test can never confuse the two.
const provenanceIdentityHeader = "X-Test-User"

// provenanceServer serves server.New against an in-memory cluster "kf" with
// the given topics and returns the handler plus the broker address, so tests
// can read the produced records straight off the broker.
func provenanceServer(t *testing.T, topics ...string) (http.Handler, string) {
	t.Helper()
	addr := startKfake(t, topics...)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "kf", Brokers: []string{addr}}}, slog.Default())
	t.Cleanup(reg.Close)
	cfg := config.Defaults()
	cfg.RBAC.Identity.Header = provenanceIdentityHeader
	return New(Options{Version: "v-test", Logger: slog.Default(), Registry: reg, Config: cfg}), addr
}

// readRecordHeaders consumes the first n records of topic from the broker and
// returns their raw headers in offset order.
func readRecordHeaders(t *testing.T, addr, topic string, n int) [][]kgo.RecordHeader {
	t.Helper()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(addr),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out [][]kgo.RecordHeader
	for len(out) < n {
		fetches := cl.PollFetches(ctx)
		require.NoError(t, ctx.Err(), "timed out after %d of %d records", len(out), n)
		fetches.EachRecord(func(r *kgo.Record) { out = append(out, r.Headers) })
	}
	require.Len(t, out, n)
	return out
}

// hdr builds a record header; the tests compare headers as unordered lists
// because kafkito emits them in map order.
func hdr(key string, value []byte) kgo.RecordHeader {
	return kgo.RecordHeader{Key: key, Value: value}
}

var binaryHeaderValue = []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0xff}

// spoofedProduceBody is a produce body that tries to plant X-Kafkito-*
// headers through every door: exact case, other cases, a Unicode case-fold
// variant, and headers_b64. trace-id, x-kafkito (no dash) and bin are
// unrelated and must reach the record unchanged.
func spoofedProduceBody(t *testing.T) []byte {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString
	body, err := json.Marshal(map[string]any{
		"value": "v",
		"headers": map[string]string{
			"X-Kafkito-User":      "mallory",
			"x-kafkito-user":      "mallory-lower",
			"X-KAFKITO-USER":      "mallory-upper",
			"X-\u212Aafkito-User": "mallory-kelvin",
			"X-Kafkito-Source":    "false",
			"x-kafkito-origin":    "elsewhere",
			"trace-id":            "abc-123",
			"x-kafkito":           "not-reserved",
		},
		"headers_b64": map[string]string{
			"X-Kafkito-User":   b64([]byte("mallory-b64")),
			"x-kafkito-source": b64([]byte("false")),
			"bin":              b64(binaryHeaderValue),
		},
	})
	require.NoError(t, err)
	return body
}

// TestProduceMessage_ReservedHeadersAreReplaced pins what the produced record
// carries: every client-supplied X-Kafkito-* header is gone, kafkito's own
// stamps are present exactly once, and unrelated headers keep their bytes.
func TestProduceMessage_ReservedHeadersAreReplaced(t *testing.T) {
	t.Parallel()

	unrelated := []kgo.RecordHeader{
		hdr("trace-id", []byte("abc-123")),
		hdr("x-kafkito", []byte("not-reserved")),
		hdr("bin", binaryHeaderValue),
	}

	for _, tc := range []struct {
		name     string
		identity string
		want     []kgo.RecordHeader
	}{
		{
			name: "no_identity_drops_the_spoofed_user",
			want: append([]kgo.RecordHeader{hdr("X-Kafkito-Source", []byte("true"))}, unrelated...),
		},
		{
			name:     "identity_replaces_the_spoofed_user",
			identity: "alice",
			want: append([]kgo.RecordHeader{
				hdr("X-Kafkito-Source", []byte("true")),
				hdr("X-Kafkito-User", []byte("alice")),
			}, unrelated...),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, addr := provenanceServer(t, "produced")

			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/kf/topics/produced/messages", bytes.NewReader(spoofedProduceBody(t)))
			req.Header.Set("Content-Type", "application/json")
			if tc.identity != "" {
				req.Header.Set(provenanceIdentityHeader, tc.identity)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			got := readRecordHeaders(t, addr, "produced", 1)[0]
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

// TestCopyMessages_SourceProvenanceIsNotCarriedOver pins that a copied record
// never inherits the original producer's X-Kafkito-* headers: the copier is
// who kafkito stamps, and the source's other headers keep their bytes.
func TestCopyMessages_SourceProvenanceIsNotCarriedOver(t *testing.T) {
	t.Parallel()
	h, addr := provenanceServer(t, "copy-src", "copy-dst")

	src, err := kgo.NewClient(kgo.SeedBrokers(addr), kgo.DefaultProduceTopic("copy-src"))
	require.NoError(t, err)
	defer src.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, src.ProduceSync(ctx, &kgo.Record{
		Value: []byte("v"),
		Headers: []kgo.RecordHeader{
			hdr("X-Kafkito-Source", []byte("true")),
			hdr("X-Kafkito-User", []byte("original-producer")),
			hdr("x-kafkito-user", []byte("lower")),
			hdr("X-KAFKITO-USER", []byte("upper")),
			// Not valid UTF-8, so the consumer hands it over in HeadersB64.
			hdr("x-kafkito-trace", binaryHeaderValue),
			hdr("origin", []byte("keep-me")),
			hdr("bin", binaryHeaderValue),
		},
	}).FirstErr())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/kf/topics/copy-src/copy",
		strings.NewReader(`{"dest_cluster":"kf","dest_topic":"copy-dst"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(provenanceIdentityHeader, "bob")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"done":true`)
	require.Contains(t, rec.Body.String(), `"copied":1`)

	got := readRecordHeaders(t, addr, "copy-dst", 1)[0]
	assert.ElementsMatch(t, []kgo.RecordHeader{
		hdr("X-Kafkito-Source", []byte("true")),
		hdr("X-Kafkito-User", []byte("bob")),
		hdr("origin", []byte("keep-me")),
		hdr("bin", binaryHeaderValue),
	}, got)
}
