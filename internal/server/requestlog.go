// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// slowRequestThreshold promotes otherwise successful requests from Debug to
// Info so slow calls stay visible at the default log level.
const slowRequestThreshold = 2 * time.Second

// maxInboundRequestIDLen bounds client-supplied request ids so a caller cannot
// bloat every log line.
const maxInboundRequestIDLen = 128

// requestIDHeader is the response header that echoes the request id.
const requestIDHeader = "X-Request-Id"

type requestInfoCtxKey struct{}

// requestInfo is shared by pointer between the request-log middleware and
// inner middlewares. Inner middlewares derive new request contexts that the
// outer middleware never sees, so values the log line needs (the principal)
// are written back into this holder.
type requestInfo struct {
	id   string
	user string
}

func requestInfoFromContext(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(requestInfoCtxKey{}).(*requestInfo)
	return info
}

// requestIDFromContext returns the id assigned by requestLogMiddleware, or ""
// outside of a request.
func requestIDFromContext(ctx context.Context) string {
	if info := requestInfoFromContext(ctx); info != nil {
		return info.id
	}
	return ""
}

// requestLogMiddleware assigns a request id, echoes it in X-Request-Id and
// emits one "http request" log line per request after the handler returns.
//
// It must run outside middleware.Recoverer so a recovered panic is logged
// with the 500 the recoverer writes. It also installs a chi LogEntry so the
// recoverer reports panics through slog instead of printing to stderr.
//
// Only method, route pattern, status, size, duration, user and cluster name
// are logged: never query strings, headers or bodies, which can carry
// credentials or message payloads.
func requestLogMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			info := &requestInfo{id: inboundRequestID(r.Header)}
			w.Header().Set(requestIDHeader, info.id)

			ctx := context.WithValue(r.Context(), requestInfoCtxKey{}, info)
			r = middleware.WithLogEntry(r.WithContext(ctx), panicLogEntry{log: log, ctx: ctx})

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			if skipRequestLog(r.URL.Path, status) {
				return
			}

			elapsed := time.Since(start)
			level := slog.LevelDebug
			slow := elapsed > slowRequestThreshold
			switch {
			case status >= 500:
				level = slog.LevelWarn
			case status >= 400, slow:
				level = slog.LevelInfo
			}
			if !log.Enabled(ctx, level) {
				return
			}

			route, cluster := r.URL.Path, ""
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if p := rctx.RoutePattern(); p != "" {
					route = p
				}
				cluster = rctx.URLParam("cluster")
			}

			attrs := []slog.Attr{
				slog.String("request_id", info.id),
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", status),
				slog.Int64("duration_ms", elapsed.Milliseconds()),
				slog.Int("bytes", ww.BytesWritten()),
				slog.String("user", info.user),
				slog.String("cluster", cluster),
			}
			if slow {
				attrs = append(attrs, slog.Bool("slow", true))
			}
			log.LogAttrs(ctx, level, "http request", attrs...)
		})
	}
}

// skipRequestLog keeps probe and static-asset noise out of the small CF log
// buffer. Rule: health probes are never logged; any path outside the backend
// prefixes (/api, /rpc, /user-api) is SPA shell or a static asset and is only
// logged when it fails (status >= 400).
func skipRequestLog(path string, status int) bool {
	if path == "/healthz" || path == "/readyz" {
		return true
	}
	return !isBackendPrefix(path) && status < 400
}

// capturePrincipal copies the authenticated principal into the request-log
// holder. Mount it directly after the auth middleware.
func capturePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info := requestInfoFromContext(r.Context()); info != nil {
			if p, ok := auth.PrincipalFromContext(r.Context()); ok && p != nil {
				info.user = p.UserName
				if info.user == "" {
					info.user = p.Subject
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// inboundRequestID picks the request id in order of preference: the CF
// gorouter's X-Vcap-Request-Id, the trace-id of a W3C traceparent,
// X-Request-Id, or a freshly generated one.
func inboundRequestID(h http.Header) string {
	if id := h.Get("X-Vcap-Request-Id"); validRequestID(id) {
		return id
	}
	if id := traceIDFromTraceparent(h.Get("Traceparent")); id != "" {
		return id
	}
	if id := h.Get(requestIDHeader); validRequestID(id) {
		return id
	}
	return newRequestID()
}

// traceIDFromTraceparent extracts the trace-id from a W3C traceparent value
// ("00-<32 hex trace-id>-<16 hex parent-id>-<2 hex flags>"). Returns "" for
// malformed or all-zero trace ids.
func traceIDFromTraceparent(v string) string {
	parts := strings.Split(strings.TrimSpace(v), "-")
	if len(parts) < 4 || len(parts[0]) != 2 || len(parts[1]) != 32 {
		return ""
	}
	id := strings.ToLower(parts[1])
	if _, err := hex.DecodeString(id); err != nil || id == strings.Repeat("0", 32) {
		return ""
	}
	return id
}

// validRequestID accepts short ids made of characters that are safe to echo
// in a header and log verbatim (UUIDs, hex, base64url-ish tokens).
func validRequestID(id string) bool {
	if id == "" || len(id) > maxInboundRequestIDLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// panicLogEntry routes middleware.Recoverer's panic report through slog. The
// per-request summary is written by requestLogMiddleware, so Write is a no-op.
type panicLogEntry struct {
	log *slog.Logger
	ctx context.Context
}

func (panicLogEntry) Write(int, int, http.Header, time.Duration, any) {}

func (e panicLogEntry) Panic(v any, stack []byte) {
	if stack == nil {
		stack = debug.Stack()
	}
	e.log.ErrorContext(e.ctx, "panic recovered",
		"request_id", requestIDFromContext(e.ctx),
		"panic", fmt.Sprint(v),
		"stack", string(stack),
	)
}

// requestIDHandler adds request_id to every record logged with a request
// context (the *Context logging methods), so handler error logs such as
// errorWriter's correlate with the request-log line without every call site
// passing the id explicitly.
type requestIDHandler struct {
	slog.Handler
}

func (h requestIDHandler) Handle(ctx context.Context, rec slog.Record) error {
	if id := requestIDFromContext(ctx); id != "" {
		rec = rec.Clone()
		rec.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, rec)
}

func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
}

// withRequestIDLogging wraps log so *Context calls carry the request id.
func withRequestIDLogging(log *slog.Logger) *slog.Logger {
	if log == nil {
		return nil
	}
	return slog.New(requestIDHandler{log.Handler()})
}
