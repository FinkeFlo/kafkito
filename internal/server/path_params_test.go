// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/oapi-codegen/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// pathParamFields are the path parameters RBAC resolves resources from.
var pathParamFields = []string{"Cluster", "Topic", "Group", "Subject", "User"}

// boundParams is what the generated binding handed to the handler.
type boundParams struct {
	calls  int
	values map[string]string
}

// recordBoundParams is a strict middleware that records the bound path
// parameters and answers 418 without running the handler.
func recordBoundParams(got *boundParams) gen.StrictMiddlewareFunc {
	return func(_ gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
		return func(_ context.Context, _ http.ResponseWriter, _ *http.Request, req any) (any, error) {
			got.calls++
			got.values = map[string]string{}
			v := reflect.ValueOf(req)
			for _, f := range pathParamFields {
				if fv := v.FieldByName(f); fv.IsValid() {
					got.values[f] = fv.String()
				}
			}
			return nil, &apiError{Status: http.StatusTeapot, Message: "recorded"}
		}
	}
}

// pathParamServer is server.New with RBAC granting userMallory every action
// on resource, logging to the returned buffer.
func pathParamServer(t *testing.T, resource string, got *boundParams) (http.Handler, *kafkapkg.Registry, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "c", Brokers: []string{"127.0.0.1:1"}}}, logger)
	t.Cleanup(reg.Close)
	cfg := config.Defaults()
	cfg.RBAC = config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
		Roles:    []config.RoleConfig{{Name: "scoped", Permissions: []config.PermissionConfig{{Resource: resource, Actions: []string{"*"}}}}},
		Subjects: []config.SubjectConfig{{User: userMallory, Roles: []string{"scoped"}}},
	}
	h := New(Options{Version: "test", Logger: logger, Registry: reg, Config: cfg, strictMiddlewares: []gen.StrictMiddlewareFunc{recordBoundParams(got)}})
	return h, reg, logs
}

// pathParamKinds are one operation per path parameter kind, with the RBAC
// resource type that parameter names. %s is the parameter's raw segment.
var pathParamKinds = []struct {
	field, resType, method, path string
}{
	{"Cluster", "cluster", http.MethodGet, "/api/v1/clusters/%s/capabilities"},
	{"Topic", "topic", http.MethodGet, "/api/v1/clusters/c/topics/%s"},
	{"Group", "group", http.MethodGet, "/api/v1/clusters/c/groups/%s"},
	{"Subject", "schema", http.MethodGet, "/api/v1/clusters/c/schemas/subjects/%s/versions"},
	{"User", "user", http.MethodDelete, "/api/v1/clusters/c/users/%s"},
}

// pathParamRequest builds a request whose path parameter is the raw,
// possibly percent-encoded segment. The request line is parsed like the
// net/http server parses it, so r.URL.RawPath is set whenever the segment
// is not in its default encoding.
func pathParamRequest(t *testing.T, method, pathFmt, segment string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, fmt.Sprintf(pathFmt, segment), http.NoBody)
	req.Header.Set(rbacTestHeader, userMallory)
	return req
}

// RBAC decides on the path parameters exactly as the generated binding
// decodes them, so the handler never acts on a name RBAC did not check.
func TestPathParams_RBACSeesBoundValue(t *testing.T) {
	t.Parallel()

	for _, k := range pathParamKinds {
		for _, tc := range []struct {
			name, resource, segment string
			wantStatus              int
			wantBound               string
		}{
			// An escaped form of an allowed name is that name.
			{"escaped allowed name", "data", "%64ata", http.StatusTeapot, "data"},
			{"fully escaped allowed name", "data", "%64%61%74%61", http.StatusTeapot, "data"},
			{"plain allowed name", "data", "data", http.StatusTeapot, "data"},
			// Policy globs are prefixes: the raw "data%4a" starts with
			// "data%4", the decoded "dataJ" does not.
			{"raw matches glob, decoded does not", "data%4*", "data%4a", http.StatusForbidden, ""},
			{"raw matches exactly, decoded does not", "data%4a", "data%4a", http.StatusForbidden, ""},
			{"raw does not match, decoded does", "dataJ", "data%4a", http.StatusTeapot, "dataJ"},
			{"decoded matches glob", "dat*", "%64ataJ", http.StatusTeapot, "dataJ"},
			// The raw "%64ata" must not be what RBAC checks.
			{"raw form is not the name", "%64ata", "%64ata", http.StatusForbidden, ""},
			// A literal percent sign is decoded once, not twice.
			{"escaped percent", "a%41", "a%2541", http.StatusTeapot, "a%41"},
			{"escaped percent not double decoded", "aA", "a%2541", http.StatusForbidden, ""},
		} {
			t.Run(k.field+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				var got boundParams
				h, _, _ := pathParamServer(t, k.resType+":"+tc.resource, &got)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, pathParamRequest(t, k.method, k.path, tc.segment))
				require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
				if tc.wantStatus != http.StatusTeapot {
					assert.Zero(t, got.calls, "handler must not run")
					return
				}
				require.Equal(t, 1, got.calls)
				assert.Equal(t, tc.wantBound, got.values[k.field])
			})
		}
	}
}

// Group and subject names may contain "/": "%2F" keeps it inside one
// segment, RBAC and the handler both see the decoded "/", and "%252F" is
// the literal "%2F".
func TestPathParams_EncodedSlash(t *testing.T) {
	t.Parallel()

	for _, k := range pathParamKinds {
		if k.field != "Group" && k.field != "Subject" {
			continue
		}
		for _, tc := range []struct {
			name, resource, segment string
			wantStatus              int
			wantBound               string
		}{
			{"slash allowed", "team/a", "team%2Fa", http.StatusTeapot, "team/a"},
			{"slash by glob", "team/*", "team%2Fa", http.StatusTeapot, "team/a"},
			{"double escaped is not a slash", "team/a", "team%252Fa", http.StatusForbidden, ""},
			{"double escaped allowed as literal", "team%2Fa", "team%252Fa", http.StatusTeapot, "team%2Fa"},
			{"literal not granted by slash name", "team%2Fa", "team%2Fa", http.StatusForbidden, ""},
		} {
			t.Run(k.field+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				var got boundParams
				h, _, _ := pathParamServer(t, k.resType+":"+tc.resource, &got)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, pathParamRequest(t, k.method, k.path, tc.segment))
				require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
				if tc.wantStatus != http.StatusTeapot {
					assert.Zero(t, got.calls, "handler must not run")
					return
				}
				require.Equal(t, 1, got.calls)
				assert.Equal(t, tc.wantBound, got.values[k.field])
			})
		}
	}
}

// A parameter that is not valid percent-encoding is a 400 before RBAC
// decides, whether the policy would allow everything or nothing; with RBAC
// disabled the binding rejects it.
func TestPathParams_InvalidEscape(t *testing.T) {
	t.Parallel()

	for _, k := range pathParamKinds {
		for _, resource := range []string{"*", "nothing-matches"} {
			t.Run(k.field+"/"+resource, func(t *testing.T) {
				t.Parallel()
				var got boundParams
				h, _, _ := pathParamServer(t, resource, &got)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, invalidEscapeRequest(t, k.method, k.path))
				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				param := strings.ToLower(k.field)
				assert.JSONEq(t, `{"code":"invalid_request","error":"invalid parameter \"`+param+`\""}`, rec.Body.String())
				assert.Zero(t, got.calls, "handler must not run")
			})
		}
	}

	t.Run("rbac disabled", func(t *testing.T) {
		t.Parallel()
		var got boundParams
		reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "c", Brokers: []string{"127.0.0.1:1"}}}, slog.Default())
		t.Cleanup(reg.Close)
		h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: config.Defaults(), strictMiddlewares: []gen.StrictMiddlewareFunc{recordBoundParams(&got)}})
		for _, k := range pathParamKinds {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, invalidEscapeRequest(t, k.method, k.path))
			assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", k.field, rec.Body.String())
		}
		assert.Zero(t, got.calls, "handler must not run")
	})

	// On the wire, net/http itself rejects the request line.
	t.Run("wire", func(t *testing.T) {
		t.Parallel()
		var got boundParams
		h, _, _ := pathParamServer(t, "*", &got)
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), 5*time.Second)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
		_, err = fmt.Fprintf(conn, "GET /api/v1/clusters/c/topics/da%%zzta HTTP/1.1\r\nHost: x\r\n%s: %s\r\n\r\n", rbacTestHeader, userMallory)
		require.NoError(t, err)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Zero(t, got.calls, "handler must not run")
	})
}

// invalidEscapeRequest is a request whose routed (raw) path parameter is
// "da%zzta". net/http never parses such a request line, so the raw path is
// set directly, as a proxy or an embedding router could.
func invalidEscapeRequest(t *testing.T, method, pathFmt string) *http.Request {
	t.Helper()
	req := pathParamRequest(t, method, pathFmt, "data")
	req.URL.RawPath = fmt.Sprintf(pathFmt, "da%zzta")
	return req
}

// An escaped private-cluster sentinel is the sentinel: it needs the
// header, bypasses RBAC only as a private cluster and reaches the handler
// under the ad-hoc registry name, never under the sentinel or a raw form.
func TestPathParams_EscapedPrivateClusterSentinel(t *testing.T) {
	t.Parallel()

	sentinel := config.PrivateClusterSentinel
	for _, segment := range []string{
		sentinel,
		"%5F" + sentinel[1:],
		strings.ReplaceAll(sentinel, "_", "%5F"),
		"%5f" + sentinel[1:],
	} {
		t.Run(segment, func(t *testing.T) {
			t.Parallel()
			var got boundParams
			// No grant at all: only the private-cluster bypass lets it through.
			h, reg, logs := pathParamServer(t, "nothing:matches", &got)
			path := "/api/v1/clusters/" + segment + "/topics/orders"

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, pathParamRequest(t, http.MethodGet, "%s", path))
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.JSONEq(t, `{"error":"private cluster requires X-Kafkito-Cluster header"}`, rec.Body.String())
			assert.Zero(t, got.calls)

			cfg := config.ClusterConfig{Brokers: []string{unreachableBroker}}
			header := encodeHeader(t, cfg)
			adhoc, err := reg.UseAdhoc(cfg)
			require.NoError(t, err)
			req := pathParamRequest(t, http.MethodGet, "%s", path)
			req.Header.Set(PrivateClusterHeader, header)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusTeapot, rec.Code, rec.Body.String())
			require.Equal(t, 1, got.calls)
			assert.Equal(t, adhoc, got.values["Cluster"])
			assert.Equal(t, "orders", got.values["Topic"])

			// The request log names the ad-hoc cluster, never the header.
			assert.Contains(t, logs.String(), `"cluster":"`+adhoc+`"`)
			assert.NotContains(t, logs.String(), header)
		})
	}

	t.Run("near sentinel is a normal cluster", func(t *testing.T) {
		t.Parallel()
		var got boundParams
		h, _, _ := pathParamServer(t, "nothing:matches", &got)
		req := pathParamRequest(t, http.MethodGet, "/api/v1/clusters/%s/capabilities", sentinel+"%41")
		req.Header.Set(PrivateClusterHeader, encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Zero(t, got.calls)
	})
}

// The request log records the cluster as the handler saw it.
func TestPathParams_RequestLogUsesBoundCluster(t *testing.T) {
	t.Parallel()

	var got boundParams
	h, _, logs := pathParamServer(t, "cluster:prod", &got)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pathParamRequest(t, http.MethodGet, "/api/v1/clusters/%s/capabilities", "%70rod"))
	require.Equal(t, http.StatusTeapot, rec.Code, rec.Body.String())
	assert.Equal(t, "prod", got.values["Cluster"])
	assert.Contains(t, logs.String(), `"cluster":"prod"`)
	assert.NotContains(t, logs.String(), `"cluster":"%70rod"`)
}

// pathParam follows the generated binding's rule: decode once when chi
// routed on the raw path, use the value as is otherwise.
func TestPathParam_MatchesBindingRule(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ target, want string }{
		{"/t/plain", "plain"},
		{"/t/a%20b", "a b"},
		{"/t/%6Frders", "orders"},
		{"/t/a%2Fb", "a/b"},
		{"/t/a%252Fb", "a%2Fb"},
		{"/t/a%25b", "a%b"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			t.Parallel()
			var mw, bound string
			var mwErr error
			r := newChiWithParam(func(r *http.Request) {
				mw, mwErr = pathParam(r, "p")
			}, &bound)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, http.NoBody))
			require.NoError(t, mwErr)
			assert.Equal(t, tc.want, mw)
			assert.Equal(t, tc.want, bound, "generated binding")
		})
	}
}

// newChiWithParam routes /t/{p}, runs observe as middleware would and binds
// {p} like the generated wrappers do (see TestGeneratedBinding_PathParamRule).
func newChiWithParam(observe func(*http.Request), bound *string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.CleanPath)
	r.Get("/t/{p}", func(_ http.ResponseWriter, req *http.Request) {
		observe(req)
		_ = runtime.BindStyledParameterWithOptions("simple", "p", chi.URLParam(req, "p"), bound, runtime.BindStyledParameterOptions{ParamLocation: runtime.ParamLocationPath, Explode: false, Required: true, Type: "string", Format: "", ValueIsUnescaped: req.URL.RawPath == ""})
	})
	return r
}

// pathParam relies on the generated wrappers binding every path parameter
// with ValueIsUnescaped: r.URL.RawPath == "". A codegen upgrade that changes
// the rule must fail here, not silently split RBAC from the handler.
func TestGeneratedBinding_PathParamRule(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("api/server.gen.go")
	require.NoError(t, err)
	n := 0
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "ParamLocation: runtime.ParamLocationPath") {
			continue
		}
		n++
		assert.Contains(t, line, `chi.URLParam(r, "`)
		assert.Contains(t, line, `ValueIsUnescaped: r.URL.RawPath == ""`)
	}
	assert.Positive(t, n)
}
