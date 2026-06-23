# Backend Security Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every backend-security finding from the 2026-06-23 audit — RBAC identity spoofing (Critical), private-cluster SSRF (High), plaintext Schema-Registry credentials (Medium), unsanitized JSON error body (Medium), goja resource amplification (Medium), gateway error-detail leakage (Low), and the weak `off`-auth-mode guard (Low).

**Architecture:** Each finding is an independent task that ends in a compiling, tested deliverable a reviewer can accept or reject on its own. The two highest-risk tasks (RBAC subject from verified JWT; SSRF guard for private-cluster URLs) come first. All fixes stay within existing packages (`internal/server`, `internal/auth`, `pkg/kafka`, `cmd/kafkito`) and add **no new runtime dependencies** — only Go stdlib (`net`, `net/url`, `net/netip`, `encoding/json`).

**Tech Stack:** Go 1.x, chi/v5 router, `github.com/dop251/goja` (already vendored), testify (`assert`/`require`), `slog`. Tests follow the existing co-located `_test.go` patterns (table-driven, `t.Parallel()`, `httptest`).

## Global Constraints

- No new runtime dependencies without explicit approval in the PR description. (All tasks here use only the Go stdlib + already-present modules.)
- Code comments and any user-facing strings are English only. No emojis in logs, comments, or commit messages.
- `go test ./...` and `golangci-lint run` (or `make test && make lint`) MUST pass before every commit.
- Follow the conventions of the surrounding Go code (copyright header on new files, table-driven `t.Parallel()` tests, testify `require`/`assert`).
- These are backend-only changes under `cmd/`, `internal/`, `pkg/`. The frontend hard-gate does not apply; no `frontend/` files are touched.

---

### Task 1: RBAC identity from the verified JWT principal (Critical)

**Problem:** `rbacMiddleware` and four sibling handlers resolve the RBAC subject from the client-supplied `X-Kafkito-User` header (`policy.Header()`), which is never reconciled with the cryptographically-verified JWT principal. Any authenticated caller can send `X-Kafkito-User: <any-admin>` and assume that user's roles. Fix: when a verified principal is present in context, it is authoritative (the inbound header is ignored); fall back to the header only when no principal exists (auth disabled).

**Files:**
- Modify: `internal/server/rbac.go` (add `rbacSubject` helper; use it at `:34`; align `handleMe` at `:178-199`)
- Modify: `internal/server/clusters.go:178`, `internal/server/clusters.go:375` (use helper)
- Modify: `internal/server/topic_consumers.go:24`, `internal/server/topic_consumers.go:63` (use helper)
- Test: `internal/server/rbac_test.go` (add subject-resolution + spoofing tests)

**Interfaces:**
- Consumes: `auth.PrincipalFromContext(ctx) (*auth.Principal, bool)`, `auth.Principal{Subject, UserName string}`, `auth.WithPrincipal(ctx, *Principal) context.Context`, `(*rbac.Policy).Header() string`.
- Produces: `func rbacSubject(r *http.Request, policy *rbac.Policy) string` — the single source of truth for the RBAC identity, used by the middleware and every handler that needs the caller's identity.

- [ ] **Step 1: Write the failing test**

Add to `internal/server/rbac_test.go` (the file already imports `auth`? No — add the import `"github.com/FinkeFlo/kafkito/internal/auth"` to its import block):

```go
func TestRBACSubject_VerifiedPrincipalOverridesHeader(t *testing.T) {
	t.Parallel()

	policy := policyAllowAll() // identity header is rbacTestHeader ("X-Test-User")

	t.Run("principal_username_wins_over_spoofed_header", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(rbacTestHeader, "attacker-admin")
		req = req.WithContext(auth.WithPrincipal(req.Context(),
			&auth.Principal{Subject: "sub-123", UserName: "real-user"}))

		assert.Equal(t, "real-user", rbacSubject(req, policy),
			"verified UserName must override the client-supplied header")
	})

	t.Run("principal_subject_used_when_no_username", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(rbacTestHeader, "attacker-admin")
		req = req.WithContext(auth.WithPrincipal(req.Context(),
			&auth.Principal{Subject: "sub-123"}))

		assert.Equal(t, "sub-123", rbacSubject(req, policy),
			"Subject must be used when UserName is empty")
	})

	t.Run("header_used_only_when_no_principal", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(rbacTestHeader, "header-user")

		assert.Equal(t, "header-user", rbacSubject(req, policy),
			"header is the fallback only when no verified principal is present")
	})
}

func TestRBACMiddleware_DeniesHeaderSpoofWhenPrincipalLacksRole(t *testing.T) {
	t.Parallel()

	// policyAllowAll grants admin only to userAdmin ("alice"); mallory has no role.
	policy := policyAllowAll()
	called := false
	leaf := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	// inject a verified principal of "mallory" BEFORE the rbac middleware runs
	r := chi.NewRouter()
	r.Group(func(g chi.Router) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := auth.WithPrincipal(req.Context(), &auth.Principal{UserName: userMallory})
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		g.Use(rbacMiddleware(policy))
		g.Delete("/clusters/{cluster}/topics/{topic}", leaf)
	})

	req := httptest.NewRequest(http.MethodDelete, "/clusters/"+clusterShared+"/topics/orders", nil)
	req.Header.Set(rbacTestHeader, userAdmin) // spoof admin via header
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "spoofed admin header must not grant access")
	assert.False(t, called, "leaf handler must not run for a denied request")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run 'TestRBACSubject_VerifiedPrincipalOverridesHeader|TestRBACMiddleware_DeniesHeaderSpoofWhenPrincipalLacksRole' -v`
Expected: FAIL to compile with `undefined: rbacSubject`.

- [ ] **Step 3: Add the `rbacSubject` helper and use it in the middleware**

In `internal/server/rbac.go`, add the helper just below the `withSubject` function (after line 26):

```go
// rbacSubject resolves the RBAC identity for the request. A verified JWT
// principal (set by auth.Middleware) is authoritative: when present, the
// client-supplied identity header is ignored to prevent header-spoofing
// privilege escalation. UserName is preferred over Subject to match handleMe.
// The header is consulted only when no principal exists (auth disabled).
func rbacSubject(r *http.Request, policy *rbac.Policy) string {
	if p, ok := auth.PrincipalFromContext(r.Context()); ok {
		if p.UserName != "" {
			return p.UserName
		}
		return p.Subject
	}
	return r.Header.Get(policy.Header())
}
```

Then replace line 34 in `rbacMiddleware`:

```go
		user := r.Header.Get(policy.Header())
```

with:

```go
		user := rbacSubject(r, policy)
```

- [ ] **Step 4: Use the helper in the four handler call sites**

In `internal/server/clusters.go`, replace both occurrences (`:178` and `:375`):

```go
		user := r.Header.Get(a.policy.Header())
```

with:

```go
		user := rbacSubject(r, a.policy)
```

In `internal/server/topic_consumers.go`, replace both occurrences (`:24` and `:63`) identically:

```go
		user := r.Header.Get(a.policy.Header())
```

with:

```go
		user := rbacSubject(r, a.policy)
```

- [ ] **Step 5: Align `handleMe` to use the same resolution**

In `internal/server/rbac.go`, the `handleMe` else-branch (line 197-199) currently reads the header directly. It already prefers the principal, so behavior matches, but route it through the helper for a single source of truth. Replace lines 188-199:

```go
		if p, ok := auth.PrincipalFromContext(r.Context()); ok {
			hasJWT = true
			user = p.Subject
			if p.UserName != "" {
				user = p.UserName
			}
			email = p.Email
			scopes = p.Scopes
			tenant = p.Tenant
		} else {
			user = r.Header.Get(policy.Header())
		}
```

with:

```go
		if p, ok := auth.PrincipalFromContext(r.Context()); ok {
			hasJWT = true
			email = p.Email
			scopes = p.Scopes
			tenant = p.Tenant
		}
		// rbacSubject is the single identity resolver: principal first, header fallback.
		user = rbacSubject(r, policy)
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/server/ -run 'TestRBAC' -v`
Expected: PASS (all RBAC tests, including the two new ones and the existing `TestRBACMiddleware_DispatchBranches`).

- [ ] **Step 7: Run the full package suite + vet**

Run: `go test ./internal/server/... && go vet ./internal/server/...`
Expected: PASS, no vet diagnostics.

- [ ] **Step 8: Commit**

```bash
git add internal/server/rbac.go internal/server/clusters.go internal/server/topic_consumers.go internal/server/rbac_test.go
git commit -m "fix(security): derive RBAC subject from verified JWT principal, not client header"
```

---

### Task 2: Build the auth 401 error body with json.Marshal (Medium)

**Problem:** `auth.deny` builds the JSON 401 body by string concatenation (`internal/auth/middleware.go:48`). All callers currently pass static literals, but the pattern is injection-fragile if a future caller passes attacker-influenced text. Fix: marshal a struct.

**Files:**
- Modify: `internal/auth/middleware.go:44-49`
- Test: `internal/auth/middleware_test.go` (add a body-shape test)

**Interfaces:**
- Consumes: nothing new.
- Produces: `deny` keeps the same signature `func deny(w http.ResponseWriter, msg string)`; the emitted body is now produced by `json.Marshal` and stays valid even for messages containing quotes.

- [ ] **Step 1: Write the failing test**

Add to `internal/auth/middleware_test.go` (ensure imports include `encoding/json`, `net/http`, `net/http/httptest`, `testing`, and testify):

```go
func TestDeny_EmitsValidJSONEvenWithQuotesInMessage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	deny(rec, `weird "quoted" message`)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"body must be valid JSON even when msg contains quotes")
	assert.Equal(t, "unauthorized", body.Error)
	assert.Equal(t, `weird "quoted" message`, body.Message)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/ -run TestDeny_EmitsValidJSONEvenWithQuotesInMessage -v`
Expected: FAIL — `json.Unmarshal` errors on the malformed body produced by string concatenation.

- [ ] **Step 3: Replace the concatenated body with json.Marshal**

In `internal/auth/middleware.go`, add `"encoding/json"` to the import block, then replace lines 44-49:

```go
func deny(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="kafkito"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","message":"` + msg + `"}`))
}
```

with:

```go
func deny(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="kafkito"`)
	w.WriteHeader(http.StatusUnauthorized)
	body, _ := json.Marshal(map[string]string{"error": "unauthorized", "message": msg})
	_, _ = w.Write(body)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/auth/ -run TestDeny_EmitsValidJSONEvenWithQuotesInMessage -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/middleware.go internal/auth/middleware_test.go
git commit -m "fix(security): marshal auth 401 error body instead of string concatenation"
```

---

### Task 3: Refuse Schema-Registry basic-auth over plaintext HTTP (Medium)

**Problem:** `SchemaRegistryClient.do` calls `req.SetBasicAuth` whenever a username is configured, including when the SR URL is `http://` (`pkg/kafka/schema_registry.go:120-122`), sending credentials in cleartext. Fix: when the client has credentials but the base URL is not `https://`, fail the request with a clear error before sending.

**Files:**
- Modify: `pkg/kafka/schema_registry.go` (add a guard in `do`, before building the request)
- Test: `pkg/kafka/schema_registry_test.go` (create if absent; add a guard test)

**Interfaces:**
- Consumes: `SchemaRegistryClient.cfg config.SchemaRegistryConfig` (fields `URL`, `Username`, `Password`).
- Produces: a sentinel error `var ErrInsecureSchemaRegistryAuth = errors.New("schema registry basic auth requires https")` returned by `do` (and therefore by every SR call) when credentials would travel over plaintext.

- [ ] **Step 1: Write the failing test**

Create or append to `pkg/kafka/schema_registry_test.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

func TestSchemaRegistryDo_RefusesBasicAuthOverPlaintextHTTP(t *testing.T) {
	t.Parallel()

	c := newSchemaRegistryClient(config.SchemaRegistryConfig{
		URL:      "http://sr.internal:8081",
		Username: "user",
		Password: "secret",
	})

	err := c.do(context.Background(), http.MethodGet, "/subjects", nil, nil)

	require.Error(t, err, "credentials over http must be refused before sending")
	assert.True(t, errors.Is(err, ErrInsecureSchemaRegistryAuth),
		"want ErrInsecureSchemaRegistryAuth, got %v", err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/kafka/ -run TestSchemaRegistryDo_RefusesBasicAuthOverPlaintextHTTP -v`
Expected: FAIL to compile with `undefined: ErrInsecureSchemaRegistryAuth` (and, once defined but unguarded, the call would attempt a real network dial instead of returning the sentinel).

- [ ] **Step 3: Add the sentinel error and the guard**

In `pkg/kafka/schema_registry.go`, add the sentinel next to `ErrNoSchemaRegistry` (after line 25):

```go
// ErrInsecureSchemaRegistryAuth is returned when basic-auth credentials are
// configured for a non-HTTPS schema registry URL; sending them would leak the
// password in cleartext, so the request is refused.
var ErrInsecureSchemaRegistryAuth = errors.New("schema registry basic auth requires https")
```

Then in `do`, immediately at the top of the function body (before `var rdr io.Reader` at line 104), add:

```go
	if c.cfg.Username != "" && !strings.HasPrefix(strings.ToLower(c.cfg.URL), "https://") {
		return ErrInsecureSchemaRegistryAuth
	}
```

(`strings` and `errors` are already imported in this file.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/kafka/ -run TestSchemaRegistryDo_RefusesBasicAuthOverPlaintextHTTP -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/kafka/schema_registry.go pkg/kafka/schema_registry_test.go
git commit -m "fix(security): refuse schema registry basic auth over plaintext http"
```

---

### Task 4: SSRF guard for private-cluster Schema-Registry URL and brokers (High)

**Problem:** `validatePrivateClusterConfig` validates only broker presence and SASL type, not `SchemaRegistry.URL` (`internal/server/private_cluster.go:84`). A caller using the `__private__` sentinel + `X-Kafkito-Cluster` header can point the SR URL at internal services or the cloud metadata endpoint (`169.254.169.254`), and the server returns the body — authenticated SSRF that also bypasses RBAC. Fix: reject SR URLs and broker hosts whose target resolves to loopback, link-local (incl. cloud metadata), or unspecified addresses, at validation time AND at dial time (defeating DNS rebinding) for the SR HTTP client. Private RFC-1918 ranges remain allowed because legitimate private clusters live there; only clearly-internal/metadata ranges are blocked.

**Files:**
- Create: `internal/server/ssrf.go` (URL/host guard helpers)
- Create: `internal/server/ssrf_test.go`
- Modify: `internal/server/private_cluster.go:84-104` (call the guard from `validatePrivateClusterConfig`)

**Interfaces:**
- Consumes: `config.ClusterConfig{Brokers []string, SchemaRegistry config.SchemaRegistryConfig{URL string}}`, stdlib `net`, `net/url`, `net/netip`.
- Produces:
  - `func blockedIP(ip netip.Addr) bool` — true for loopback, link-local unicast/multicast, unspecified, and the `169.254.169.254` metadata address.
  - `func validateOutboundHost(host string) error` — resolves `host` and returns an error if any resolved address is blocked.
  - `func validateOutboundURL(raw string) error` — parses `raw`, requires http/https scheme, and runs `validateOutboundHost` on its hostname.

- [ ] **Step 1: Write the failing test**

Create `internal/server/ssrf_test.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

func TestBlockedIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},      // loopback
		{"::1", true},            // loopback v6
		{"169.254.169.254", true}, // cloud metadata (link-local)
		{"169.254.1.1", true},    // link-local
		{"0.0.0.0", true},        // unspecified
		{"10.0.0.5", false},      // private but legitimate for private clusters
		{"192.168.1.10", false},  // private but legitimate
		{"8.8.8.8", false},       // public
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()
			addr, err := netip.ParseAddr(tc.ip)
			require.NoError(t, err)
			assert.Equal(t, tc.blocked, blockedIP(addr))
		})
	}
}

func TestValidateOutboundURL(t *testing.T) {
	t.Parallel()

	assert.Error(t, validateOutboundURL("http://127.0.0.1:8081"), "loopback must be blocked")
	assert.Error(t, validateOutboundURL("http://169.254.169.254/latest/meta-data/"), "metadata must be blocked")
	assert.Error(t, validateOutboundURL("ftp://example.com"), "non-http(s) scheme must be blocked")
	assert.Error(t, validateOutboundURL("://nonsense"), "unparseable url must be blocked")
	assert.NoError(t, validateOutboundURL("https://schema-registry.example.com:8081"), "public host must pass")
}

func TestValidatePrivateClusterConfig_BlocksSSRFSchemaRegistry(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Brokers: []string{"broker.example.com:9092"},
		Auth:    config.AuthConfig{Type: "none"},
		SchemaRegistry: config.SchemaRegistryConfig{
			URL: "http://169.254.169.254/latest/meta-data/",
		},
	}

	err := validatePrivateClusterConfig(cfg)

	assert.Error(t, err, "SR URL pointing at the metadata endpoint must be rejected")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run 'TestBlockedIP|TestValidateOutboundURL|TestValidatePrivateClusterConfig_BlocksSSRFSchemaRegistry' -v`
Expected: FAIL to compile with `undefined: blockedIP` / `undefined: validateOutboundURL`.

- [ ] **Step 3: Implement the SSRF guard helpers**

Create `internal/server/ssrf.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// metadataIP is the cloud instance-metadata address (AWS/GCP/Azure). It is
// link-local and therefore already covered by blockedIP, but named for clarity.
var metadataIP = netip.MustParseAddr("169.254.169.254")

// blockedIP reports whether an outbound connection to ip must be refused to
// prevent SSRF. Loopback, link-local (incl. the cloud metadata endpoint),
// multicast, and unspecified addresses are blocked. Private RFC-1918 ranges
// are intentionally allowed: legitimate private clusters live there.
func blockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	switch {
	case ip == metadataIP:
		return true
	case ip.IsLoopback():
		return true
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return true
	case ip.IsUnspecified():
		return true
	case ip.IsMulticast():
		return true
	default:
		return false
	}
}

// validateOutboundHost resolves host (a "host" or "host:port") and returns an
// error if it is empty or any resolved address is blocked.
func validateOutboundHost(host string) error {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty host")
	}
	// Literal IP: check directly.
	if addr, err := netip.ParseAddr(host); err == nil {
		if blockedIP(addr) {
			return fmt.Errorf("host %q resolves to a blocked address", host)
		}
		return nil
	}
	// Hostname: resolve and check every address.
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, a := range addrs {
		addr, perr := netip.ParseAddr(a)
		if perr != nil {
			continue
		}
		if blockedIP(addr) {
			return fmt.Errorf("host %q resolves to a blocked address %s", host, a)
		}
	}
	return nil
}

// validateOutboundURL parses raw, requires an http(s) scheme, and validates the
// host against the SSRF block-list.
func validateOutboundURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("url scheme %q not allowed", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url has no host")
	}
	return validateOutboundHost(u.Host)
}
```

- [ ] **Step 4: Call the guard from validatePrivateClusterConfig**

In `internal/server/private_cluster.go`, extend `validatePrivateClusterConfig`. Replace the broker loop and add the SR check — replace lines 85-103 (the body before the final `return nil`):

```go
	if len(cfg.Brokers) == 0 {
		return errors.New("at least one broker is required")
	}
	for _, b := range cfg.Brokers {
		if strings.TrimSpace(b) == "" {
			return errors.New("broker address must not be empty")
		}
	}
	t := strings.ToLower(strings.TrimSpace(cfg.Auth.Type))
	switch t {
	case "", "none":
	case "plain", "scram-sha-256", "scram-sha-512":
		if cfg.Auth.Username == "" || cfg.Auth.Password == "" {
			return fmt.Errorf("auth %q requires username and password", t)
		}
	default:
		return fmt.Errorf("auth.type %q not supported", cfg.Auth.Type)
	}
	return nil
```

with:

```go
	if len(cfg.Brokers) == 0 {
		return errors.New("at least one broker is required")
	}
	for _, b := range cfg.Brokers {
		if strings.TrimSpace(b) == "" {
			return errors.New("broker address must not be empty")
		}
		if err := validateOutboundHost(strings.TrimSpace(b)); err != nil {
			return fmt.Errorf("broker %q: %w", b, err)
		}
	}
	t := strings.ToLower(strings.TrimSpace(cfg.Auth.Type))
	switch t {
	case "", "none":
	case "plain", "scram-sha-256", "scram-sha-512":
		if cfg.Auth.Username == "" || cfg.Auth.Password == "" {
			return fmt.Errorf("auth %q requires username and password", t)
		}
	default:
		return fmt.Errorf("auth.type %q not supported", cfg.Auth.Type)
	}
	if u := strings.TrimSpace(cfg.SchemaRegistry.URL); u != "" {
		if err := validateOutboundURL(u); err != nil {
			return fmt.Errorf("schema_registry.url: %w", err)
		}
	}
	return nil
```

- [ ] **Step 5: Run the SSRF tests + the existing private-cluster suite**

Run: `go test ./internal/server/ -run 'SSRF|BlockedIP|ValidateOutbound|PrivateCluster|DecodePrivateClusterHeader' -v`
Expected: PASS. Note: the existing `TestDecodePrivateClusterHeader_*` cases use broker `localhost:9092` / `a:1`. `localhost` resolves to `127.0.0.1`, which is now blocked. **Confirm and reconcile:** if `TestDecodePrivateClusterHeader_AcceptsValidConfig` or `TestPrivateClusterMiddleware_*` now fail because of `localhost`/`a:1`, update those fixtures to a non-loopback host (e.g. `broker.example.com:9092`) in `internal/server/private_cluster_test.go`. This is expected fallout of the guard, not a regression — record the fixture change in the commit.

- [ ] **Step 6: Reconcile existing fixtures if needed**

If Step 5 reported failures in the existing tests, edit `internal/server/private_cluster_test.go`: replace broker fixtures `"localhost:9092"`, `"a:1"`, `"b:1"`, `"a:0"`, `"x:1"`, `"other:9092"` with non-loopback resolvable-shaped hosts that pass the literal-IP fast path or a public-shaped name — simplest is a literal public IP like `"203.0.113.10:9092"` (TEST-NET-3, never routable but not blocked). Re-run Step 5 until green. Do NOT weaken `blockedIP` to make tests pass.

- [ ] **Step 7: Run vet**

Run: `go vet ./internal/server/...`
Expected: no diagnostics.

- [ ] **Step 8: Commit**

```bash
git add internal/server/ssrf.go internal/server/ssrf_test.go internal/server/private_cluster.go internal/server/private_cluster_test.go
git commit -m "fix(security): block SSRF in private-cluster schema registry URL and brokers"
```

---

### Task 5: Bound goja JS-filter resource use across a scan (Medium)

**Problem:** `jsMatcher.match` allocates a fresh `goja.New()` runtime and re-runs `RunProgram` for every record, plus spawns a goroutine + unstoppable `time.After` timer per message (`pkg/kafka/search_matchers.go:244-256`). Over a budget of up to 500k records this is a CPU/allocation amplification vector. Fix: build the runtime and extract the callable once per matcher instance (one per search), reuse them, and use a stoppable timer with `ClearInterrupt` between calls. The matcher is used sequentially in the single-goroutine poll loop (`search.go:435`), so reuse is safe.

**Files:**
- Modify: `pkg/kafka/search_matchers.go:220-286`
- Test: `pkg/kafka/search_matchers_test.go` (create if absent; add reuse + timeout tests)

**Interfaces:**
- Consumes: `goja.Compile`, `goja.New`, `goja.AssertFunction`, `goja.Callable`, `(*Message)` fields `Key, Value, Headers, Partition, Offset, Timestamp`.
- Produces: `jsMatcher` gains fields `rt *goja.Runtime` and `fn goja.Callable`; `match` keeps the interface signature `match(m *Message) (bool, error)` so it still satisfies the `matcher` interface — no caller changes.

- [ ] **Step 1: Write the failing test**

Create or append to `pkg/kafka/search_matchers_test.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSMatcher_ReusesRuntimeAcrossMessages(t *testing.T) {
	t.Parallel()

	m, err := newJSMatcher(`value.includes("yes")`)
	require.NoError(t, err)

	rtBefore := m.rt
	require.NotNil(t, rtBefore, "runtime must be built once at construction")

	hit, err := m.match(&Message{Value: "say yes"})
	require.NoError(t, err)
	assert.True(t, hit)

	miss, err := m.match(&Message{Value: "say no"})
	require.NoError(t, err)
	assert.False(t, miss)

	assert.Same(t, rtBefore, m.rt, "runtime must be reused, not reallocated per message")
}

func TestJSMatcher_RecoversAfterTimeout(t *testing.T) {
	t.Parallel()

	// A pathological expression that would loop; the per-call interrupt must
	// abort it, and the reused runtime must still serve the next message.
	m, err := newJSMatcher(`(function(){ while(true){} })()`)
	require.NoError(t, err)

	_, err = m.match(&Message{Value: "x"})
	assert.Error(t, err, "runaway script must be interrupted")

	// ClearInterrupt must have run so a normal call still works afterwards.
	hit, err := newJSMatcher(`value === "ok"`)
	require.NoError(t, err)
	got, err := hit.match(&Message{Value: "ok"})
	require.NoError(t, err)
	assert.True(t, got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/kafka/ -run 'TestJSMatcher_ReusesRuntimeAcrossMessages|TestJSMatcher_RecoversAfterTimeout' -v`
Expected: FAIL to compile — `jsMatcher` has no `rt` field yet.

- [ ] **Step 3: Rewrite jsMatcher to reuse the runtime**

In `pkg/kafka/search_matchers.go`, replace the struct and both functions (lines 220-286):

```go
type jsMatcher struct {
	prog *goja.Program
	src  string
}

func newJSMatcher(script string) (*jsMatcher, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, errors.New("js filter requires a non-empty expression")
	}
	body := script
	if !strings.Contains(body, "return") {
		body = "return (" + body + ");"
	}
	wrapped := "(function(key, value, parsed, headers, partition, offset, timestampMs){ " + body + " })"
	prog, err := goja.Compile("kafkito-filter.js", wrapped, true)
	if err != nil {
		return nil, fmt.Errorf("js filter compile: %w", err)
	}
	return &jsMatcher{prog: prog, src: script}, nil
}

func (j *jsMatcher) match(m *Message) (bool, error) {
	rt := goja.New()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-time.After(100 * time.Millisecond):
			rt.Interrupt("kafkito-filter timeout")
		case <-done:
		}
	}()

	v, err := rt.RunProgram(j.prog)
	if err != nil {
		return false, fmt.Errorf("js filter run: %w", err)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return false, errors.New("js filter did not compile to a function")
	}
	var parsed any
	if m.Value != "" {
		_ = json.Unmarshal([]byte(m.Value), &parsed)
	}
	headers := make(map[string]string, len(m.Headers))
	for k, v := range m.Headers {
		headers[k] = v
	}
	res, err := fn(goja.Undefined(),
		rt.ToValue(m.Key),
		rt.ToValue(m.Value),
		rt.ToValue(parsed),
		rt.ToValue(headers),
		rt.ToValue(m.Partition),
		rt.ToValue(m.Offset),
		rt.ToValue(m.Timestamp),
	)
	if err != nil {
		return false, fmt.Errorf("js filter run: %w", err)
	}
	return res.ToBoolean(), nil
}
```

with:

```go
type jsMatcher struct {
	rt  *goja.Runtime
	fn  goja.Callable
	src string
}

func newJSMatcher(script string) (*jsMatcher, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, errors.New("js filter requires a non-empty expression")
	}
	body := script
	if !strings.Contains(body, "return") {
		body = "return (" + body + ");"
	}
	wrapped := "(function(key, value, parsed, headers, partition, offset, timestampMs){ " + body + " })"
	prog, err := goja.Compile("kafkito-filter.js", wrapped, true)
	if err != nil {
		return nil, fmt.Errorf("js filter compile: %w", err)
	}
	// Build the runtime and extract the callable once; reused for every
	// message in the scan to avoid per-record allocation amplification.
	rt := goja.New()
	v, err := rt.RunProgram(prog)
	if err != nil {
		return nil, fmt.Errorf("js filter run: %w", err)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return nil, errors.New("js filter did not compile to a function")
	}
	return &jsMatcher{rt: rt, fn: fn, src: script}, nil
}

// match evaluates the filter against m. The runtime is reused across calls
// (the search poll loop is single-goroutine). A stoppable per-call timer arms
// an interrupt to bound a single pathological expression; ClearInterrupt resets
// the runtime so the next message is unaffected.
func (j *jsMatcher) match(m *Message) (bool, error) {
	done := make(chan struct{})
	defer close(done)
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	go func() {
		select {
		case <-timer.C:
			j.rt.Interrupt("kafkito-filter timeout")
		case <-done:
		}
	}()
	defer j.rt.ClearInterrupt()

	var parsed any
	if m.Value != "" {
		_ = json.Unmarshal([]byte(m.Value), &parsed)
	}
	headers := make(map[string]string, len(m.Headers))
	for k, v := range m.Headers {
		headers[k] = v
	}
	res, err := j.fn(goja.Undefined(),
		j.rt.ToValue(m.Key),
		j.rt.ToValue(m.Value),
		j.rt.ToValue(parsed),
		j.rt.ToValue(headers),
		j.rt.ToValue(m.Partition),
		j.rt.ToValue(m.Offset),
		j.rt.ToValue(m.Timestamp),
	)
	if err != nil {
		return false, fmt.Errorf("js filter run: %w", err)
	}
	return res.ToBoolean(), nil
}
```

- [ ] **Step 4: Run the matcher tests to verify they pass**

Run: `go test ./pkg/kafka/ -run 'TestJSMatcher' -v`
Expected: PASS (both new tests plus any existing JS-matcher tests).

- [ ] **Step 5: Run the full kafka package suite**

Run: `go test ./pkg/kafka/...`
Expected: PASS — search behavior unchanged for valid expressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/kafka/search_matchers.go pkg/kafka/search_matchers_test.go
git commit -m "perf(security): reuse goja runtime across js-filter matches to bound resource use"
```

---

### Task 6: Harden the `off` (no-auth) mode guard (Low)

**Problem:** `off` mode (no authentication) is only blocked when `VCAP_APPLICATION` is set (`cmd/kafkito/main.go:67`) — i.e. only on Cloud Foundry. Anywhere else, a misconfigured `KAFKITO_AUTH_MODE=off` silently disables auth. Fix: extract a pure, testable guard that fails `off` mode whenever a production signal is present (CF, or a non-loopback bind address) unless the operator explicitly acknowledges via `KAFKITO_INSECURE_AUTH_OFF=true`.

**Files:**
- Create: `cmd/kafkito/authguard.go` (pure guard function)
- Create: `cmd/kafkito/authguard_test.go`
- Modify: `cmd/kafkito/main.go:66-70` (call the guard)

**Interfaces:**
- Consumes: `mode string`, the resolved bind `addr string`, and an env lookup `func(string) string`.
- Produces: `func guardAuthMode(mode, addr string, env func(string) string) error` — returns a non-nil error when running `off` mode would be unsafe.

- [ ] **Step 1: Write the failing test**

Create `cmd/kafkito/authguard_test.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuardAuthMode(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cases := []struct {
		name    string
		mode    string
		addr    string
		env     map[string]string
		wantErr bool
	}{
		{name: "non_off_mode_always_ok", mode: "oidc", addr: ":8080", wantErr: false},
		{name: "off_on_loopback_ok", mode: "off", addr: "127.0.0.1:8080", wantErr: false},
		{name: "off_on_localhost_ok", mode: "off", addr: "localhost:8080", wantErr: false},
		{name: "off_on_cloudfoundry_blocked", mode: "off", addr: "127.0.0.1:8080",
			env: map[string]string{"VCAP_APPLICATION": "{}"}, wantErr: true},
		{name: "off_on_public_bind_blocked", mode: "off", addr: ":8080", wantErr: true},
		{name: "off_on_public_bind_acknowledged_ok", mode: "off", addr: ":8080",
			env: map[string]string{"KAFKITO_INSECURE_AUTH_OFF": "true"}, wantErr: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guardAuthMode(tc.mode, tc.addr, env(tc.env))
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/kafkito/ -run TestGuardAuthMode -v`
Expected: FAIL to compile with `undefined: guardAuthMode`.

- [ ] **Step 3: Implement the guard**

Create `cmd/kafkito/authguard.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package main

import (
	"errors"
	"net"
	"strings"
)

// guardAuthMode returns an error when running with mode "off" (no
// authentication) would be unsafe. "off" is allowed only when the bind address
// is loopback, unless the operator explicitly acknowledges the risk via
// KAFKITO_INSECURE_AUTH_OFF=true. Any Cloud Foundry deployment (VCAP_APPLICATION
// present) is always refused regardless of the acknowledgement.
func guardAuthMode(mode, addr string, env func(string) string) error {
	if mode != "off" {
		return nil
	}
	if env("VCAP_APPLICATION") != "" {
		return errors.New("KAFKITO_AUTH_MODE=off is forbidden when running on Cloud Foundry")
	}
	if strings.EqualFold(env("KAFKITO_INSECURE_AUTH_OFF"), "true") {
		return nil
	}
	if isLoopbackBind(addr) {
		return nil
	}
	return errors.New("KAFKITO_AUTH_MODE=off requires a loopback bind address or KAFKITO_INSECURE_AUTH_OFF=true")
}

// isLoopbackBind reports whether addr binds only to the loopback interface.
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.TrimSuffix(addr, ":")
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
```

- [ ] **Step 4: Wire the guard into main**

In `cmd/kafkito/main.go`, the `addr` is computed at line 82 (`addr := listenAddress(cfg.Server.Addr)`), which is *after* the current guard at lines 66-70. Move the guard to use the resolved `addr`. Replace lines 66-70:

```go
	// Guard: production binaries must never run "off" mode.
	if mode == "off" && os.Getenv("VCAP_APPLICATION") != "" {
		logger.Error("KAFKITO_AUTH_MODE=off is forbidden when running on Cloud Foundry")
		os.Exit(2)
	}
```

with (delete the old guard here entirely):

```go
```

Then, immediately after line 82 (`addr := listenAddress(cfg.Server.Addr)`), insert:

```go
	if err := guardAuthMode(mode, addr, os.Getenv); err != nil {
		logger.Error("insecure auth configuration", "mode", mode, "addr", addr, "err", err)
		os.Exit(2)
	}
```

(Note: `validator` is built at lines 72-80, before `addr`. That ordering is fine — the process still exits before `srv.ListenAndServe`. The guard must run before `go func(){ srv.ListenAndServe() }()` at line 95, which it does.)

- [ ] **Step 5: Run the guard test + build the command**

Run: `go test ./cmd/kafkito/ -run TestGuardAuthMode -v && go build ./cmd/kafkito`
Expected: PASS and a clean build.

- [ ] **Step 6: Commit**

```bash
git add cmd/kafkito/authguard.go cmd/kafkito/authguard_test.go cmd/kafkito/main.go
git commit -m "fix(security): require loopback or explicit ack to run auth mode off"
```

---

### Task 7: Stop leaking raw upstream error detail to clients (Low)

**Problem:** Handlers return raw broker/SR errors verbatim, e.g. `"kafka: " + err.Error()` (`internal/server/topic_consumers.go:55-57`, and similar `"kafka: "` sites in `clusters.go`), exposing internal hostnames/topology. Fix: add a logger to `clusterAPI`, log the full error server-side, and return a generic gateway message to the client.

**Files:**
- Modify: `internal/server/clusters.go:41-44` (add `log *slog.Logger` field), and the `server.go` construction site
- Modify: `internal/server/server.go:67` (pass the logger into `clusterAPI`)
- Create: `internal/server/errors.go` (the `gatewayError` helper)
- Create: `internal/server/errors_test.go`
- Modify: the `"kafka: " + err.Error()` return sites to call `gatewayError`

**Interfaces:**
- Consumes: `clusterAPI.log *slog.Logger`, `opts.Logger *slog.Logger` (already on `Options`).
- Produces: `func gatewayError(ctx context.Context, w http.ResponseWriter, log *slog.Logger, detail string, err error)` — logs `err` with `detail` server-side and writes a 502 with a generic body `{"error":"upstream kafka error","code":"kafka_upstream"}`. (Context is the first parameter, per Go convention.)

- [ ] **Step 1: Write the failing test**

Create `internal/server/errors_test.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayError_HidesDetailFromClientButLogsIt(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	rec := httptest.NewRecorder()
	secret := errors.New("dial broker-internal-7.corp.local:9092: connection refused")

	gatewayError(context.Background(), rec, log, "list consumers", secret)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "kafka_upstream", body["code"])
	assert.NotContains(t, rec.Body.String(), "broker-internal-7.corp.local",
		"internal hostname must not leak to the client")
	assert.True(t, strings.Contains(buf.String(), "broker-internal-7.corp.local"),
		"full error must be logged server-side")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestGatewayError_HidesDetailFromClientButLogsIt -v`
Expected: FAIL to compile with `undefined: gatewayError`.

- [ ] **Step 3: Implement the helper**

Create `internal/server/errors.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"log/slog"
	"net/http"
)

// gatewayError logs the full upstream error server-side (with a short context
// detail) and returns a generic 502 to the client, so internal broker/SR
// hostnames and topology are not leaked in the HTTP response.
func gatewayError(ctx context.Context, w http.ResponseWriter, log *slog.Logger, detail string, err error) {
	if log != nil {
		log.ErrorContext(ctx, "upstream kafka error", "detail", detail, "err", err)
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{
		"error": "upstream kafka error",
		"code":  "kafka_upstream",
	})
}
```

- [ ] **Step 4: Add the logger to clusterAPI and the construction site**

In `internal/server/clusters.go`, replace lines 41-44:

```go
type clusterAPI struct {
	reg    *kafkapkg.Registry
	policy *rbac.Policy
}
```

with:

```go
type clusterAPI struct {
	reg    *kafkapkg.Registry
	policy *rbac.Policy
	log    *slog.Logger
}
```

(Add `"log/slog"` to the `clusters.go` import block if not present.)

In `internal/server/server.go`, replace line 67:

```go
					(&clusterAPI{reg: opts.Registry, policy: policy}).mount(g)
```

with:

```go
					(&clusterAPI{reg: opts.Registry, policy: policy, log: opts.Logger}).mount(g)
```

- [ ] **Step 5: Route the raw-error returns through gatewayError**

In `internal/server/topic_consumers.go`, replace the default branch (lines 54-58):

```go
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "kafka: " + err.Error(),
			})
		}
```

with:

```go
		default:
			gatewayError(ctx, w, a.log, "list consumers for topic "+topic, err)
		}
```

Then search the package for the remaining leak sites and convert each:

Run: `grep -rn '"kafka: " + err.Error()' internal/server/*.go`

For every match inside a `clusterAPI` method, replace the `writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})` block with `gatewayError(r.Context(), w, a.log, "<short action>", err)` using a short English action description matching the handler (e.g. `"list topics"`, `"describe group"`). Use the local `ctx` when one is in scope, otherwise `r.Context()`.

- [ ] **Step 6: Run the package suite + vet**

Run: `go test ./internal/server/... && go vet ./internal/server/...`
Expected: PASS. If any existing test asserted on the old `"kafka: ..."` body text, update it to assert the new `"code":"kafka_upstream"` shape (record the change in the commit).

- [ ] **Step 7: Commit**

```bash
git add internal/server/errors.go internal/server/errors_test.go internal/server/clusters.go internal/server/topic_consumers.go internal/server/server.go
git commit -m "fix(security): return generic gateway errors, log upstream detail server-side"
```

---

### Final verification (after all tasks)

- [ ] **Run the full backend suite, race detector, and linter**

Run: `go test ./... && go test -race ./internal/server/... ./pkg/kafka/... && golangci-lint run`
Expected: all green. If `make test && make lint` is the project's canonical entrypoint, run that instead.

- [ ] **Confirm no new dependencies were added**

Run: `git diff main -- go.mod go.sum`
Expected: empty diff. If non-empty, stop — a runtime dependency was introduced and needs explicit PR approval per the global constraints.

---

## Out of scope (recorded, no action)

- **ReDoS via operator masking/topic regexes** (`pkg/masking/masking.go:51-68`) — patterns compile with Go RE2 (linear time, no catastrophic backtracking) and are operator-supplied config, not user input. No action; documented here so it is not re-flagged.

## Notes for the implementer

- Tasks are independent and can be implemented/reviewed in any order, but the severity order (1 → 7) is the recommended sequence; Task 1 (Critical) and Task 4 (High) gate any multi-user/hosted deployment and should ship first, ideally as their own PR.
- Task 4 changes the validation contract for private clusters: `localhost`/loopback brokers are now rejected. This is intentional. If local-dev workflows rely on `localhost` private clusters, that is a product decision to surface in the PR — the secure default is to block it.
- Every task ends green on its own; do not batch commits across tasks.
</content>
</invoke>
