// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// countingTransport counts the response body bytes the client reads.
type countingTransport struct {
	next http.RoundTripper
	read atomic.Int64
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	res.Body = &countingBody{ReadCloser: res.Body, read: &t.read}
	return res, nil
}

type countingBody struct {
	io.ReadCloser
	read *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read.Add(int64(n))
	return n, err
}

// newCountingSRClient returns a client for url and the transport that
// counts the body bytes it reads.
func newCountingSRClient(t *testing.T, url string) (*SchemaRegistryClient, *countingTransport) {
	t.Helper()
	c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: url}, false)
	ct := &countingTransport{next: c.http.Transport}
	c.http.Transport = ct
	return c, ct
}

// endlessBodyBytes is a body size the client must never read to the end.
const endlessBodyBytes = 256 << 20

// streamingHandler answers with status and a body of total bytes: head,
// then chunk repeated. It stops early when the client stops reading.
func streamingHandler(status int, head, chunk string, total int) http.HandlerFunc {
	block := []byte(strings.Repeat(chunk, (32<<10)/len(chunk)))
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if _, err := io.WriteString(w, head); err != nil {
			return
		}
		for rest := total - len(head); rest > 0 && r.Context().Err() == nil; {
			n := min(rest, len(block))
			if _, err := w.Write(block[:n]); err != nil {
				return
			}
			rest -= n
		}
	}
}

func TestSchemaRegistryDo_CapsErrorBody(t *testing.T) {
	t.Parallel()

	atCap := strings.Repeat("x", maxSRErrorBodyBytes)
	const jsonErrHead = `{"error_code":50001,"message":"`
	cases := []struct {
		name    string
		handler http.HandlerFunc
		status  int
		code    int
		message string
	}{
		{
			name: "registry_error_json",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error_code":40401,"message":"Subject not found."}`)
			},
			status:  http.StatusNotFound,
			code:    40401,
			message: "Subject not found.",
		},
		{
			name: "plain_text",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, "bad gateway\n")
			},
			status:  http.StatusBadGateway,
			message: "bad gateway",
		},
		{
			name:    "at_the_cap",
			handler: streamingHandler(http.StatusInternalServerError, "", "x", maxSRErrorBodyBytes),
			status:  http.StatusInternalServerError,
			message: atCap,
		},
		{
			name:    "one_byte_over_the_cap",
			handler: streamingHandler(http.StatusInternalServerError, "", "x", maxSRErrorBodyBytes+1),
			status:  http.StatusInternalServerError,
			message: atCap + srTruncatedMarker,
		},
		{
			name:    "endless",
			handler: streamingHandler(http.StatusInternalServerError, "", "x", endlessBodyBytes),
			status:  http.StatusInternalServerError,
			message: atCap + srTruncatedMarker,
		},
		{
			// The cut JSON does not parse, so the raw text is kept.
			name:    "endless_registry_error_json",
			handler: streamingHandler(http.StatusInternalServerError, jsonErrHead, "x", endlessBodyBytes),
			status:  http.StatusInternalServerError,
			message: jsonErrHead + atCap[len(jsonErrHead):] + srTruncatedMarker,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)
			c, ct := newCountingSRClient(t, srv.URL)

			err := c.do(context.Background(), http.MethodGet, "/subjects", nil, nil)

			var srErr *SRError
			require.ErrorAs(t, err, &srErr)
			assert.Equal(t, tc.status, srErr.Status)
			assert.Equal(t, tc.code, srErr.Code)
			assert.Equal(t, tc.message, srErr.Message)
			assert.LessOrEqual(t, ct.read.Load(), int64(maxSRErrorBodyBytes+1), "bytes read from the error body")
		})
	}
}

func TestSchemaRegistryDo_RefusesOversizedResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		ok      bool
	}{
		{name: "at_the_cap", handler: streamingHandler(http.StatusOK, `["orders"]`, " ", maxSRResponseBytes), ok: true},
		{name: "one_byte_over_the_cap", handler: streamingHandler(http.StatusOK, `["orders"]`, " ", maxSRResponseBytes+1)},
		{name: "endless_subject_list", handler: streamingHandler(http.StatusOK, "[", `"subject",`, endlessBodyBytes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)
			c, ct := newCountingSRClient(t, srv.URL)

			subjects, err := c.ListSubjects(context.Background())

			assert.LessOrEqual(t, ct.read.Load(), int64(maxSRResponseBytes+1), "bytes read from the body")
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, []string{"orders"}, subjects)
				return
			}
			require.ErrorIs(t, err, errSRResponseTooLarge)
			assert.Nil(t, subjects, "no partial document is decoded")
		})
	}
}
