// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

const privateSubjects = "/api/v1/clusters/__private__/schemas/subjects"

// privateAdminServer is server.New with no static clusters that logs to
// the returned buffer.
func privateAdminServer(t *testing.T) (http.Handler, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg := kafkapkg.NewRegistry(nil, logger)
	t.Cleanup(reg.Close)
	return New(Options{Version: "x", Logger: logger, Registry: reg, Config: config.Defaults()}), logs
}

func sendPrivate(t *testing.T, h http.Handler, header, method, path, body string, timeout time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader = http.NoBody
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(PrivateClusterHeader, header)
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// nonLoopbackIPv4 returns an IPv4 address of a local interface that is not
// a loopback address: private clusters must not reach loopback.
func nonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		return ipn.IP.String()
	}
	return ""
}

// The schema endpoints of a private cluster use the Schema Registry of the
// X-Kafkito-Cluster config, with its credentials, and neither the header
// nor the Schema Registry password shows up in a response or a log line.
func TestSchemaOps_PrivateClusterSchemaRegistry(t *testing.T) {
	t.Parallel()

	t.Run("without a schema registry", func(t *testing.T) {
		t.Parallel()
		h, logs := privateAdminServer(t)
		header := encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
		rec := sendPrivate(t, h, header, http.MethodGet, privateSubjects, "", 5*time.Second)
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"error":"schema registry not configured for cluster: `+kafkapkg.AdhocPrefix)
		assert.NotContains(t, rec.Body.String(), header)
		assert.NotContains(t, logs.String(), header)
	})

	t.Run("unreachable schema registry", func(t *testing.T) {
		t.Parallel()
		h, logs := privateAdminServer(t)
		// A black-holed private address: the request only fails because
		// the header's Schema Registry is dialled.
		header := encodeHeader(t, config.ClusterConfig{
			Brokers:        []string{unreachableBroker},
			SchemaRegistry: config.SchemaRegistryConfig{URL: "https://10.255.255.1:1", Username: "sr-user", Password: leakPassword},
		})
		for _, tc := range []struct{ method, path, body string }{
			{http.MethodGet, privateSubjects, ""},
			{http.MethodGet, privateSubjects + "/orders-value/versions", ""},
			{http.MethodGet, privateSubjects + "/orders-value/versions/latest", ""},
			{http.MethodPost, privateSubjects + "/orders-value/versions", `{"schema":"\"string\""}`},
			{http.MethodDelete, privateSubjects + "/orders-value?permanent=true", ""},
		} {
			rec := sendPrivate(t, h, header, tc.method, tc.path, tc.body, 300*time.Millisecond)
			assert.Equal(t, http.StatusBadGateway, rec.Code, "%s %s: %s", tc.method, tc.path, rec.Body.String())
			assert.JSONEq(t, `{"code":"kafka_upstream","error":"upstream kafka error"}`, rec.Body.String())
		}
		for _, secret := range []string{leakPassword, header} {
			assert.NotContains(t, logs.String(), secret)
		}
		assert.Contains(t, logs.String(), "10.255.255.1", "the upstream error is logged")
	})

	t.Run("loopback schema registry is refused", func(t *testing.T) {
		t.Parallel()
		sr := startFakeSchemaRegistry(t)
		h, _ := privateAdminServer(t)
		header := encodeHeader(t, config.ClusterConfig{
			Brokers:        []string{unreachableBroker},
			SchemaRegistry: config.SchemaRegistryConfig{URL: sr.URL},
		})
		rec := sendPrivate(t, h, header, http.MethodGet, privateSubjects, "", 5*time.Second)
		assert.GreaterOrEqual(t, rec.Code, 400, rec.Body.String())
		assert.Empty(t, sr.recorded(), "a private cluster must not reach a loopback registry")
	})

	t.Run("reachable schema registry", func(t *testing.T) {
		t.Parallel()
		ip := nonLoopbackIPv4()
		if ip == "" {
			t.Skip("no non-loopback IPv4 interface to serve a private Schema Registry on")
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
		if err != nil {
			t.Skipf("listen on %s: %v", ip, err)
		}
		var (
			authMu  sync.Mutex
			gotAuth []string
		)
		sr := &fakeSchemaRegistry{subjects: map[string][]kafkapkg.SchemaVersion{}, nextID: 1}
		sr.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, _ := r.BasicAuth()
			authMu.Lock()
			gotAuth = append(gotAuth, u+":"+p)
			authMu.Unlock()
			sr.serve(w, r)
		}))
		_ = sr.Listener.Close()
		sr.Listener = ln
		// Basic auth to a private cluster's registry requires https.
		sr.StartTLS()
		t.Cleanup(sr.Close)

		probe := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // test server
		resp, err := probe.Get(sr.URL + "/subjects")
		if err != nil {
			t.Skipf("this host cannot connect to its own address %s: %v", ip, err)
		}
		_ = resp.Body.Close()
		sr.mu.Lock()
		sr.requests = nil
		sr.mu.Unlock()
		authMu.Lock()
		gotAuth = nil
		authMu.Unlock()

		h, logs := privateAdminServer(t)
		header := encodeHeader(t, config.ClusterConfig{
			Brokers:        []string{unreachableBroker},
			SchemaRegistry: config.SchemaRegistryConfig{URL: sr.URL, Username: "sr-user", Password: leakPassword, InsecureSkipVerify: true},
		})
		rec := sendPrivate(t, h, header, http.MethodPost, privateSubjects+"/orders-value/versions", `{"schema":"\"string\""}`, 5*time.Second)
		require.Equal(t, http.StatusOK, rec.Code, "%s\n%s", rec.Body.String(), logs.String())
		assert.JSONEq(t, `{"id":1}`, rec.Body.String())

		rec = sendPrivate(t, h, header, http.MethodGet, privateSubjects, "", 5*time.Second)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"subjects":[{"name":"orders-value","versions":[1]}]`)

		rec = sendPrivate(t, h, header, http.MethodGet, privateSubjects+"/orders-value/versions/1", "", 5*time.Second)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"schema":"\"string\""`)

		rec = sendPrivate(t, h, header, http.MethodDelete, privateSubjects+"/orders-value?permanent=true", "", 5*time.Second)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, sr.recorded(), "DELETE /subjects/orders-value?permanent=true")

		authMu.Lock()
		defer authMu.Unlock()
		require.NotEmpty(t, gotAuth)
		for _, a := range gotAuth {
			assert.Equal(t, "sr-user:"+leakPassword, a, "the header's credentials are sent")
		}
		for _, secret := range []string{leakPassword, header} {
			assert.NotContains(t, logs.String(), secret)
		}
	})
}

// Validation errors of the SCRAM user, ACL, consumer group and schema
// operations name the field and the rule, never the value: neither a SCRAM
// password nor the X-Kafkito-Cluster header of the private cluster reaches
// the response or any log line, and neither does the password when the
// broker is unreachable.
func TestAdminOps_ErrorsNeverLeakCredentials(t *testing.T) {
	t.Parallel()

	header := encodeHeader(t, config.ClusterConfig{
		Brokers: []string{unreachableBroker},
		Auth:    config.AuthConfig{Type: "plain", Username: "leak-user", Password: leakPassword},
	})
	const (
		priv  = "/api/v1/clusters/__private__"
		users = priv + "/users"
	)
	scram := func(fields string) string {
		return `{"user":"u","mechanism":"SCRAM-SHA-256","password":"` + leakPassword + `"` + fields + `}`
	}
	for i, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, users, `{"user":"u","mechanism":"` + leakPassword + `","password":"` + leakPassword + `"}`, 400},
		{http.MethodPost, users, `{"user":"u","mechanism":"SCRAM-SHA-256","password":{"` + leakPassword + `":1}}`, 400},
		{http.MethodPost, users, `{"user":"u","mechanism":"SCRAM-SHA-256","password":["` + leakPassword + `"]}`, 400},
		{http.MethodPost, users, scram(`,"iterations":"` + leakPassword + `"`), 400},
		{http.MethodPost, users, scram(`,"iterations":2147483648`), 400},
		{http.MethodPost, users, `{"user":1,"password":"` + leakPassword + `"}`, 400},
		{http.MethodPost, users, `{"password":"` + leakPassword + `"}`, 400},
		{http.MethodPost, users, `{"user":"u","password":"` + leakPassword, 400},
		{http.MethodPost, users, padJSON(t, scram(""), maxSCRAMBodyBytes+1), 400},
		{http.MethodPost, users, scram(`,"iterations":4095`), 400},
		{http.MethodPost, users, `{"user":" ","mechanism":"SCRAM-SHA-512","password":"` + leakPassword + `"}`, 400},
		// Upstream failures are logged, the password is not.
		{http.MethodPost, users, scram(""), 502},
		{http.MethodDelete, users + "/u?mechanism=" + leakPassword, "", 400},
		{http.MethodPost, priv + "/acls", `{"principal":"User:u","host":{"` + leakPassword + `":1},"resource_type":"TOPIC","resource_name":"t","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`, 400},
		{http.MethodDelete, priv + "/acls", `{"principal":"` + leakPassword, 400},
		{http.MethodPost, priv + "/groups", `{"group_id":"g","topic":"t","strategy":"` + leakPassword + `"}`, 400},
		{http.MethodPost, priv + "/groups/g/reset-offsets", `{"topic":"t","strategy":"latest","offset":"` + leakPassword + `"}`, 400},
		{http.MethodPost, priv + "/schemas/subjects/s/versions", `{"schema":"x","schemaType":"` + leakPassword + `"}`, 400},
		{http.MethodDelete, priv + "/schemas/subjects/s?permanent=" + leakPassword, "", 400},
	} {
		h, logs := privateAdminServer(t)
		rec := sendPrivate(t, h, header, tc.method, tc.path, tc.body, 300*time.Millisecond)
		require.Equal(t, tc.want, rec.Code, "case %d: %s", i, rec.Body.String())
		for _, secret := range []string{leakPassword, header} {
			assert.NotContains(t, rec.Body.String(), secret, "case %d: response leaks", i)
			assert.NotContains(t, logs.String(), secret, "case %d: logs leak", i)
		}
		if tc.want == 502 {
			assert.Contains(t, logs.String(), "upsert SCRAM user", "case %d: the upstream error is logged", i)
		}
	}
}
