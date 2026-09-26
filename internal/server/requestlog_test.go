// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logSink collects JSON log records for assertions.
type logSink struct{ buf bytes.Buffer }

func newLogSink(level slog.Level) (*logSink, *slog.Logger) {
	s := &logSink{}
	return s, slog.New(slog.NewJSONHandler(&s.buf, &slog.HandlerOptions{Level: level}))
}

func (s *logSink) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(s.buf.Bytes()))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), "line=%s", sc.Text())
		out = append(out, m)
	}
	return out
}

func (s *logSink) requestRecords(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range s.records(t) {
		if m["msg"] == "http request" {
			out = append(out, m)
		}
	}
	return out
}

// testRouter mirrors the middleware order of New.
func testRouter(log *slog.Logger) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.CleanPath)
	r.Use(requestLogMiddleware(log))
	r.Use(middleware.Recoverer)
	return r
}

func TestInboundRequestID_Precedence(t *testing.T) {
	t.Parallel()
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"vcap wins", map[string]string{"X-Vcap-Request-Id": "vcap-1", "Traceparent": tp, "X-Request-Id": "rid"}, "vcap-1"},
		{"traceparent next", map[string]string{"Traceparent": tp, "X-Request-Id": "rid"}, "4bf92f3577b34da6a3ce929d0e0e4736"},
		{"x-request-id last", map[string]string{"X-Request-Id": "rid-2"}, "rid-2"},
		{"malformed traceparent skipped", map[string]string{"Traceparent": "00-zz-00f067aa0ba902b7-01", "X-Request-Id": "rid-3"}, "rid-3"},
		{"zero trace id skipped", map[string]string{"Traceparent": "00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01", "X-Request-Id": "rid-4"}, "rid-4"},
		{"unsafe id rejected", map[string]string{"X-Request-Id": "a b\nc"}, ""},
		{"overlong id rejected", map[string]string{"X-Request-Id": strings.Repeat("a", 129)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			got := inboundRequestID(h)
			if tc.want == "" {
				assert.Len(t, got, 32, "expected generated hex id, got %q", got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRequestLog_FieldsAndHeader(t *testing.T) {
	t.Parallel()
	sink, log := newLogSink(slog.LevelDebug)
	r := testRouter(log)
	r.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithPrincipal(r.Context(), &auth.Principal{Subject: "sub-1", UserName: "alice"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}, capturePrincipal).Get("/api/v1/clusters/{cluster}/topics", func(w http.ResponseWriter, r *http.Request) {
		assert.NotEmpty(t, requestIDFromContext(r.Context()))
		writeJSON(w, http.StatusOK, []string{"a"})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/topics?password=hunter2", nil)
	req.Header.Set("X-Vcap-Request-Id", "abc")
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, "abc", rec.Header().Get("X-Request-Id"))
	recs := sink.requestRecords(t)
	require.Len(t, recs, 1)
	got := recs[0]
	assert.Equal(t, "DEBUG", got["level"])
	assert.Equal(t, "abc", got["request_id"])
	assert.Equal(t, "GET", got["method"])
	assert.Equal(t, "/api/v1/clusters/{cluster}/topics", got["route"])
	assert.EqualValues(t, 200, got["status"])
	assert.EqualValues(t, rec.Body.Len(), got["bytes"])
	assert.Contains(t, got, "duration_ms")
	assert.Equal(t, "alice", got["user"])
	assert.Equal(t, "prod", got["cluster"])
	assert.NotContains(t, sink.buf.String(), "hunter2", "query strings must not be logged")
	assert.NotContains(t, sink.buf.String(), "secret-token", "headers must not be logged")
}

func TestRequestLog_UserFallsBackToSubject(t *testing.T) {
	t.Parallel()
	sink, log := newLogSink(slog.LevelDebug)
	r := testRouter(log)
	r.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), &auth.Principal{Subject: "sub-1"})))
		})
	}, capturePrincipal).Get("/api/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	recs := sink.requestRecords(t)
	require.Len(t, recs, 1)
	assert.Equal(t, "sub-1", recs[0]["user"])
}

func TestRequestLog_Levels(t *testing.T) {
	t.Parallel()
	sink, log := newLogSink(slog.LevelDebug)
	r := testRouter(log)
	r.Get("/api/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/api/bad", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })
	r.Get("/api/boom", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })

	for _, p := range []string{"/api/ok", "/api/bad", "/api/boom"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	recs := sink.requestRecords(t)
	require.Len(t, recs, 3)
	assert.Equal(t, "DEBUG", recs[0]["level"])
	assert.Equal(t, "INFO", recs[1]["level"])
	assert.Equal(t, "WARN", recs[2]["level"])
	assert.NotContains(t, recs[0], "slow")
}

func TestRequestLog_PanicLoggedAs500(t *testing.T) {
	t.Parallel()
	sink, log := newLogSink(slog.LevelInfo)
	r := testRouter(log)
	r.Get("/api/panic", func(http.ResponseWriter, *http.Request) { panic("kaboom") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/panic", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	var panicRec, reqRec map[string]any
	for _, m := range sink.records(t) {
		switch m["msg"] {
		case "panic recovered":
			panicRec = m
		case "http request":
			reqRec = m
		}
	}
	require.NotNil(t, panicRec, "recoverer must report through slog")
	assert.Equal(t, "ERROR", panicRec["level"])
	assert.Equal(t, "kaboom", panicRec["panic"])
	require.NotNil(t, reqRec)
	assert.EqualValues(t, 500, reqRec["status"])
	assert.Equal(t, "WARN", reqRec["level"])
	assert.Equal(t, panicRec["request_id"], reqRec["request_id"])
}

func TestRequestLog_SkipsProbesAndStaticAssets(t *testing.T) {
	t.Parallel()
	sink, log := newLogSink(slog.LevelDebug)
	r := testRouter(log)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	r.Get("/healthz", ok)
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/missing") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	for _, p := range []string{"/healthz", "/readyz", "/", "/assets/app.js", "/favicon.svg", "/topics/foo"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		assert.NotEmpty(t, rec.Header().Get("X-Request-Id"), p)
	}
	assert.Empty(t, sink.requestRecords(t), "probes and successful static requests must not be logged")

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	recs := sink.requestRecords(t)
	require.Len(t, recs, 1, "failed static requests are still logged")
	assert.EqualValues(t, 404, recs[0]["status"])
}

func TestRequestLog_FlushPassesThrough(t *testing.T) {
	t.Parallel()
	_, log := newLogSink(slog.LevelDebug)
	r := testRouter(log)
	r.Get("/api/stream", func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !assert.True(t, ok, "wrapped writer must implement http.Flusher") {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: 1\n\n"))
		f.Flush()
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stream", nil))
	assert.True(t, rec.Flushed)
	assert.Equal(t, "data: 1\n\n", rec.Body.String())
}

func TestUpstreamError_CarriesRequestID(t *testing.T) {
	t.Parallel()
	sink, log := newLogSink(slog.LevelInfo)
	handlerLog := withRequestIDLogging(log)
	r := testRouter(log)
	r.Get("/api/fail", func(w http.ResponseWriter, r *http.Request) {
		errorWriter{log: handlerLog}.writeError(w, r, upstreamError("list topics", errors.New("dial failed")))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/fail", nil)
	req.Header.Set("X-Request-Id", "rid-42")
	r.ServeHTTP(httptest.NewRecorder(), req)

	var found bool
	for _, m := range sink.records(t) {
		if m["msg"] == "upstream kafka error" {
			found = true
			assert.Equal(t, "rid-42", m["request_id"])
		}
	}
	assert.True(t, found)
}

func TestNew_RequestLogging(t *testing.T) {
	t.Parallel()
	sink, log := newLogSink(slog.LevelInfo)
	h := New(Options{Version: "test", Logger: log, Config: config.Config{}})

	for _, p := range []string{"/healthz", "/api/v1/info", "/api/v1/does-not-exist"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		assert.NotEmpty(t, rec.Header().Get("X-Request-Id"), p)
	}

	recs := sink.requestRecords(t)
	require.Len(t, recs, 1, "only the 404 is logged at info")
	assert.EqualValues(t, 404, recs[0]["status"])
	assert.Equal(t, "INFO", recs[0]["level"])
}
