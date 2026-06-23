# Backend Bug Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the High- and Medium-severity backend correctness bugs from the 2026-06-23 audit (parse-error fall-through, unbounded request bodies, wrong consumer-group lag, schema-cache lock contention, discarded offset-clamp errors, brittle 404 detection, and the "0 groups" metrics lie).

**Architecture:** Each bug is an independent task ending in a runnable test and a commit. Where a bug lives inside broker-dependent code (`kadm` results), the fix extracts a small pure helper so the logic is unit-testable without a live Kafka cluster. Schema-Registry behavior is tested against the existing `httptest.NewServer` fake (see `pkg/kafka/srdeserializer_test.go`).

**Tech Stack:** Go 1.x, chi/v5, `github.com/twmb/franz-go/pkg/kadm`, testify (`assert`/`require`), `httptest`. Tests follow the co-located `_test.go` table-driven + `t.Parallel()` patterns already in the repo.

## Global Constraints

- No new runtime dependencies without explicit approval in the PR description. (Every task here uses only the Go stdlib + modules already imported.)
- Code comments and user-facing strings are English only. No emojis in logs, comments, or commit messages.
- `go test ./...` and `golangci-lint run` (or `make test && make lint`) MUST pass before every commit.
- Follow surrounding Go conventions: copyright header on new files, testify `require`/`assert`, `t.Parallel()` on leaf tests.
- Backend-only: changes are under `internal/server/` and `pkg/kafka/`. No `frontend/` files are touched.
- **Already covered elsewhere — do NOT duplicate:** the `auth.deny` JSON-body bug is fixed in the security plan (Task 2); the goja per-message timer/runtime bug is fixed in the security plan (Task 5). This plan excludes both.

---

### Task 1: Parse-error fall-through in message handlers (High)

**Problem:** `consumeMessages`, `sampleMessages`, and `searchMessages` use `if err != nil { if writeParamError(w, err) { return } }` (`internal/server/clusters.go:244-249`, `:289-294`, `:758-763`). `writeParamError` returns `false` for any error that is not a `*paramError`, and on that branch execution falls through and proceeds with half-populated `opts`. It works today only because the parse helpers return solely `*paramError` — a fragile invariant. Fix: `writeParamError` always responds (a generic 400 for non-`paramError`) and the callers always `return` on error.

**Files:**
- Modify: `internal/server/message_params.go:37-45` (`writeParamError`)
- Modify: `internal/server/clusters.go:244-249`, `:289-294`, `:758-763` (three call sites)
- Test: `internal/server/message_params_test.go` (add a writeParamError test)

**Interfaces:**
- Consumes: `paramError{status int, msg string}`, `badParam(msg string) *paramError`.
- Produces: `writeParamError(w http.ResponseWriter, err error) bool` keeps its signature but now ALWAYS writes a response and ALWAYS returns `true` when `err != nil` (a non-`paramError` yields a generic 400). Callers become `if err != nil { writeParamError(w, err); return }`.

- [ ] **Step 1: Write the failing test**

Add to `internal/server/message_params_test.go` (ensure `errors`, `net/http`, `net/http/httptest`, `encoding/json`, `testing`, testify are imported):

```go
func TestWriteParamError_HandlesNonParamErrorWithGeneric400(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	handled := writeParamError(rec, errors.New("some unexpected non-param error"))

	assert.True(t, handled, "writeParamError must report it handled the error so the caller returns")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "non-paramError must still produce a 400, not fall through")

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"], "a client-facing error message must be present")
}

func TestWriteParamError_PreservesParamErrorStatusAndMessage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	handled := writeParamError(rec, badParam("partition must be an integer"))

	assert.True(t, handled)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "partition must be an integer")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestWriteParamError -v`
Expected: `TestWriteParamError_HandlesNonParamErrorWithGeneric400` FAILS — currently returns `false` and writes nothing (Code is 200).

- [ ] **Step 3: Make writeParamError always respond**

In `internal/server/message_params.go`, replace lines 37-45:

```go
// writeParamError writes a paramError as JSON and returns true if it handled the error.
func writeParamError(w http.ResponseWriter, err error) bool {
	var pe *paramError
	if errors.As(err, &pe) {
		writeJSON(w, pe.status, map[string]string{"error": pe.msg})
		return true
	}
	return false
}
```

with:

```go
// writeParamError writes err as a JSON error response and returns true so the
// caller can return immediately. A *paramError keeps its specific status and
// message; any other error is reported as a generic 400 so a malformed request
// can never fall through into handler logic with partially-parsed options.
func writeParamError(w http.ResponseWriter, err error) bool {
	var pe *paramError
	if errors.As(err, &pe) {
		writeJSON(w, pe.status, map[string]string{"error": pe.msg})
		return true
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request parameters"})
	return true
}
```

- [ ] **Step 4: Fix the three call sites**

In `internal/server/clusters.go`, replace each of the three blocks (at `:244-249`, `:289-294`, `:758-763`) — they are identical:

```go
	if err != nil {
		if writeParamError(w, err) {
			return
		}
	}
```

with:

```go
	if err != nil {
		writeParamError(w, err)
		return
	}
```

(There are exactly three occurrences. Verify with `grep -n 'if writeParamError(w, err)' internal/server/clusters.go` — it must return nothing after the edit.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/server/ -run 'TestWriteParamError|TestResolvePermission|TestRBAC' -v && go vet ./internal/server/...`
Expected: PASS, no vet diagnostics.

- [ ] **Step 6: Commit**

```bash
git add internal/server/message_params.go internal/server/clusters.go internal/server/message_params_test.go
git commit -m "fix: always return on message-handler parse errors instead of falling through"
```

---

### Task 2: Cap the message-search request body (High)

**Problem:** `parseSearchBody` decodes `r.Body` unbounded via `json.NewDecoder(r.Body)` (`internal/server/message_params.go`), while every sibling JSON handler wraps the body in `http.MaxBytesReader` (e.g. `clusters.go:497`). An oversized POST to `/messages/search` is buffered into memory → DoS. Fix: thread the `ResponseWriter` into `parseSearchBody` and cap the body.

**Files:**
- Modify: `internal/server/message_params.go` (`parseSearchBody` signature + body cap)
- Modify: `internal/server/clusters.go:758` (call site passes `w`)
- Test: `internal/server/message_params_test.go`

**Interfaces:**
- Consumes: `http.MaxBytesReader(w, r.Body, n)`.
- Produces: `parseSearchBody(w http.ResponseWriter, r *http.Request) (kafkapkg.SearchOptions, error)` — same return shape; an over-cap body yields a `*paramError` (so Task 1's `writeParamError` turns it into a 400). New package const `maxSearchBodyBytes = 1 << 20`.

- [ ] **Step 1: Write the failing test**

Add to `internal/server/message_params_test.go`:

```go
func TestParseSearchBody_RejectsOversizedBody(t *testing.T) {
	t.Parallel()

	huge := `{"value":"` + strings.Repeat("A", (1<<20)+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/x/messages/search", strings.NewReader(huge))
	rec := httptest.NewRecorder()

	_, err := parseSearchBody(rec, req)

	require.Error(t, err, "a body larger than the cap must be rejected")
	var pe *paramError
	assert.True(t, errors.As(err, &pe), "over-cap body must surface as a client paramError, got %T", err)
}

func TestParseSearchBody_AcceptsSmallValidBody(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/x/messages/search",
		strings.NewReader(`{"value":"hello","mode":"contains"}`))
	rec := httptest.NewRecorder()

	opts, err := parseSearchBody(rec, req)

	require.NoError(t, err)
	assert.Equal(t, "hello", opts.Value)
}
```

(Add `"strings"` to the test file's imports if not already present.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestParseSearchBody -v`
Expected: FAIL to compile — `parseSearchBody` currently takes only `*http.Request`.

- [ ] **Step 3: Add the cap and change the signature**

In `internal/server/message_params.go`, add the const near the top (after the imports, before `paramError`):

```go
// maxSearchBodyBytes bounds the search request body to protect against memory
// exhaustion, matching the cap used by the other JSON handlers.
const maxSearchBodyBytes = 1 << 20
```

Then change `parseSearchBody`'s signature and first lines from:

```go
func parseSearchBody(r *http.Request) (kafkapkg.SearchOptions, error) {
	var body searchRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return kafkapkg.SearchOptions{}, badParam("invalid json body: " + err.Error())
	}
```

to:

```go
func parseSearchBody(w http.ResponseWriter, r *http.Request) (kafkapkg.SearchOptions, error) {
	var body searchRequestBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSearchBodyBytes))
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return kafkapkg.SearchOptions{}, badParam("invalid json body: " + err.Error())
	}
```

- [ ] **Step 4: Update the call site**

In `internal/server/clusters.go:758` (inside `searchMessages`), change:

```go
	opts, err := parseSearchBody(r)
```

to:

```go
	opts, err := parseSearchBody(w, r)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/server/ -run TestParseSearchBody -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/message_params.go internal/server/clusters.go internal/server/message_params_test.go
git commit -m "fix: cap message-search request body with MaxBytesReader"
```

---

### Task 3: Wrong consumer-group lag on partition errors (High)

**Problem:** `DescribeGroup` reads the log-end offset with `if eo, ok := ends.Lookup(topic, p); ok { logEnd = eo.Offset }` (`pkg/kafka/groups.go:227-229`), checking only `ok`. `kadm`'s `Lookup` returns `ok=true` for partition entries carrying a per-partition error (the offset is a sentinel). The sibling `groupLag` correctly also guards `eo.Err == nil` / `eo.Offset >= 0`. Fix: extract a guarded helper and use it in both places.

**Files:**
- Modify: `pkg/kafka/groups.go` (add `logEndOffset` helper; use it at `:227-229`)
- Test: `pkg/kafka/groups_test.go` (create if absent)

**Interfaces:**
- Consumes: `kadm.ListedOffsets` (map type `map[string]map[int32]kadm.ListedOffset`; each `ListedOffset` has `Offset int64`, `Err error`, `Topic string`, `Partition int32`).
- Produces: `func logEndOffset(ends kadm.ListedOffsets, topic string, p int32) int64` — returns the log-end offset, or `-1` when the partition is missing, carries an error, or has a negative offset.

- [ ] **Step 1: Write the failing test**

Create `pkg/kafka/groups_test.go` (or append if it exists):

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kadm"
)

func TestLogEndOffset(t *testing.T) {
	t.Parallel()

	ends := kadm.ListedOffsets{
		"orders": {
			0: kadm.ListedOffset{Topic: "orders", Partition: 0, Offset: 42},
			1: kadm.ListedOffset{Topic: "orders", Partition: 1, Offset: 99, Err: errors.New("leader unavailable")},
			2: kadm.ListedOffset{Topic: "orders", Partition: 2, Offset: -1},
		},
	}

	assert.Equal(t, int64(42), logEndOffset(ends, "orders", 0), "healthy partition returns its offset")
	assert.Equal(t, int64(-1), logEndOffset(ends, "orders", 1), "partition with an error must not return its sentinel offset")
	assert.Equal(t, int64(-1), logEndOffset(ends, "orders", 2), "negative offset must be treated as unknown")
	assert.Equal(t, int64(-1), logEndOffset(ends, "orders", 9), "missing partition is unknown")
	assert.Equal(t, int64(-1), logEndOffset(ends, "other", 0), "missing topic is unknown")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/kafka/ -run TestLogEndOffset -v`
Expected: FAIL to compile — `undefined: logEndOffset`.

- [ ] **Step 3: Add the helper and use it**

In `pkg/kafka/groups.go`, add the helper (place it just above `DescribeGroup` or with the other unexported helpers):

```go
// logEndOffset returns the log-end offset for topic/partition p, or -1 when the
// partition is missing, carries a per-partition error, or reports a negative
// (sentinel) offset. kadm's Lookup reports ok=true even for error entries, so
// the Err and sign checks are required to avoid computing bogus lag.
func logEndOffset(ends kadm.ListedOffsets, topic string, p int32) int64 {
	if eo, ok := ends.Lookup(topic, p); ok && eo.Err == nil && eo.Offset >= 0 {
		return eo.Offset
	}
	return -1
}
```

Then replace the buggy block at `pkg/kafka/groups.go:225-229`:

```go
			logEnd := int64(-1)
			if eo, ok := ends.Lookup(topic, p); ok {
				logEnd = eo.Offset
			}
```

with:

```go
			logEnd := logEndOffset(ends, topic, p)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/kafka/ -run TestLogEndOffset -v && go vet ./pkg/kafka/...`
Expected: PASS, no vet diagnostics.

- [ ] **Step 5: Commit**

```bash
git add pkg/kafka/groups.go pkg/kafka/groups_test.go
git commit -m "fix: guard log-end offset lookup against partition errors in DescribeGroup"
```

---

### Task 4: Schema cache holds the write lock across the registry HTTP call (Medium)

**Problem:** `SRDecoder.lookup` takes `d.mu.Lock()` with `defer Unlock` and then performs the blocking `GetSchemaByID` HTTP call inside the lock (`pkg/kafka/srdeserializer.go:118-152`). A slow/hung Schema Registry serializes every concurrent `Decode` — even cache hits for unrelated IDs. Fix: re-check under the write lock, release it during the fetch, then re-lock only to store.

**Files:**
- Modify: `pkg/kafka/srdeserializer.go:118-152` (`lookup`)
- Test: `pkg/kafka/srdeserializer_test.go` (add a non-blocking-fetch test)

**Interfaces:**
- Consumes: `d.mu sync.RWMutex`, `d.cache map[uint32]srEntry`, `d.sr.GetSchemaByID(ctx, int) (*SchemaVersion, error)`.
- Produces: `lookup(ctx, id uint32) (srEntry, error)` keeps its signature; behavior is unchanged except the registry fetch no longer holds `d.mu`.

- [ ] **Step 1: Write the failing test**

Add to `pkg/kafka/srdeserializer_test.go` (it already imports `net/http`, `net/http/httptest`; add `"context"`, `"sync"`, `"time"` if missing):

```go
func TestSRDecoderLookup_FetchDoesNotHoldLockForOtherIDs(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas/ids/1", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-release // block until the test releases id=1
		_, _ = w.Write([]byte(`{"schema":"\"string\"","schemaType":"AVRO","subject":"s","version":1}`))
	})
	mux.HandleFunc("/schemas/ids/2", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"schema":"\"string\"","schemaType":"AVRO","subject":"s","version":1}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sr := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL})
	dec := NewSRDecoder(sr)

	// Goroutine A blocks fetching id=1.
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = dec.lookup(context.Background(), 1)
	}()
	<-started

	// id=2 must resolve even while id=1's fetch is in flight (no global lock held during fetch).
	done := make(chan error, 1)
	go func() {
		_, err := dec.lookup(context.Background(), 2)
		done <- err
	}()

	select {
	case err := <-done:
		assert.NoError(t, err, "id=2 lookup must complete while id=1 fetch is blocked")
	case <-time.After(2 * time.Second):
		t.Fatal("id=2 lookup blocked behind id=1 fetch — write lock is still held during the HTTP call")
	}
	close(release)
}
```

(Add `"sync/atomic"` to the imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/kafka/ -run TestSRDecoderLookup_FetchDoesNotHoldLockForOtherIDs -v`
Expected: FAIL with the 2-second timeout — id=2 is blocked behind id=1's fetch because the write lock spans the HTTP call.

- [ ] **Step 3: Release the lock during the fetch**

In `pkg/kafka/srdeserializer.go`, replace the body of `lookup` (lines 118-152):

```go
func (d *SRDecoder) lookup(ctx context.Context, id uint32) (srEntry, error) {
	d.mu.RLock()
	if e, ok := d.cache[id]; ok {
		d.mu.RUnlock()
		return e, nil
	}
	d.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()
	// re-check under write lock
	if e, ok := d.cache[id]; ok {
		return e, nil
	}

	sv, err := d.sr.GetSchemaByID(ctx, int(id))
	if err != nil {
		return srEntry{}, fmt.Errorf("fetch schema id %d: %w", id, err)
	}
	entry := srEntry{
		id:         sv.ID,
		subject:    sv.Subject,
		version:    sv.Version,
		schemaType: sv.SchemaType,
	}
	if formatFromSchemaType(sv.SchemaType) == "avro" {
		parsed, perr := avro.Parse(sv.Schema)
		if perr != nil {
			return srEntry{}, fmt.Errorf("parse avro schema id %d: %w", id, perr)
		}
		entry.parsedAvro = parsed
	}
	d.cache[id] = entry
	return entry, nil
}
```

with:

```go
func (d *SRDecoder) lookup(ctx context.Context, id uint32) (srEntry, error) {
	d.mu.RLock()
	e, ok := d.cache[id]
	d.mu.RUnlock()
	if ok {
		return e, nil
	}

	// Fetch WITHOUT holding the lock so a slow registry does not serialize
	// decoding of unrelated schema IDs.
	sv, err := d.sr.GetSchemaByID(ctx, int(id))
	if err != nil {
		return srEntry{}, fmt.Errorf("fetch schema id %d: %w", id, err)
	}
	entry := srEntry{
		id:         sv.ID,
		subject:    sv.Subject,
		version:    sv.Version,
		schemaType: sv.SchemaType,
	}
	if formatFromSchemaType(sv.SchemaType) == "avro" {
		parsed, perr := avro.Parse(sv.Schema)
		if perr != nil {
			return srEntry{}, fmt.Errorf("parse avro schema id %d: %w", id, perr)
		}
		entry.parsedAvro = parsed
	}

	d.mu.Lock()
	// Another goroutine may have stored the same id while we fetched; prefer
	// the existing entry to keep a single canonical value.
	if existing, ok := d.cache[id]; ok {
		d.mu.Unlock()
		return existing, nil
	}
	d.cache[id] = entry
	d.mu.Unlock()
	return entry, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/kafka/ -run 'TestSRDecoder' -race -v`
Expected: PASS, including the new test and the existing Avro roundtrip; `-race` clean.

- [ ] **Step 5: Commit**

```bash
git add pkg/kafka/srdeserializer.go pkg/kafka/srdeserializer_test.go
git commit -m "fix: do not hold schema-cache write lock during registry fetch"
```

---

### Task 5: Discarded offset-clamp errors in ResetOffsets (Medium)

**Problem:** `ResetOffsets` discards the errors from the bounds lookups (`ends, _ := adm.ListEndOffsets(...)` / `starts, _ := adm.ListStartOffsets(...)` at `pkg/kafka/admin_groups.go:136-137`). On a transient failure, `ends`/`starts` are nil, clamping silently no-ops, and an out-of-range absolute or shifted offset is committed unclamped. Fix: extract a pure clamp helper (unit-tested) and gate the offset/shift strategies on bounds availability, surfacing an error instead of committing unclamped.

**Files:**
- Modify: `pkg/kafka/admin_groups.go` (add `clampToBounds` helper; capture bounds errors; gate ToOffset/ShiftBy)
- Test: `pkg/kafka/admin_groups_test.go` (create if absent)

**Interfaces:**
- Consumes: `kadm.ListedOffsets`, `ResetOffsetResult{Partition, OldOffset, NewOffset, EndOffset int64, Error string}`, strategies `ResetToOffset`, `ResetShiftBy`.
- Produces: `func clampToBounds(off int64, starts, ends kadm.ListedOffsets, topic string, p int32) int64` — clamps `off` into `[start, end]` using only entries with `Err == nil`; leaves a side unbounded when that side is missing.

- [ ] **Step 1: Write the failing test**

Create `pkg/kafka/admin_groups_test.go`:

```go
// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kadm"
)

func TestClampToBounds(t *testing.T) {
	t.Parallel()

	starts := kadm.ListedOffsets{"t": {0: kadm.ListedOffset{Topic: "t", Partition: 0, Offset: 100}}}
	ends := kadm.ListedOffsets{"t": {0: kadm.ListedOffset{Topic: "t", Partition: 0, Offset: 200}}}

	assert.Equal(t, int64(150), clampToBounds(150, starts, ends, "t", 0), "in-range stays")
	assert.Equal(t, int64(100), clampToBounds(50, starts, ends, "t", 0), "below start clamps up")
	assert.Equal(t, int64(200), clampToBounds(999, starts, ends, "t", 0), "above end clamps down")

	// Error entries must be ignored (treated as no bound on that side).
	errEnds := kadm.ListedOffsets{"t": {0: kadm.ListedOffset{Topic: "t", Partition: 0, Offset: 200, Err: errors.New("boom")}}}
	assert.Equal(t, int64(999), clampToBounds(999, starts, errEnds, "t", 0), "errored end bound is ignored")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/kafka/ -run TestClampToBounds -v`
Expected: FAIL to compile — `undefined: clampToBounds`.

- [ ] **Step 3: Add the helper, capture errors, and gate the strategies**

In `pkg/kafka/admin_groups.go`, add the helper near the other unexported helpers:

```go
// clampToBounds clamps off into [start, end] for topic/partition p, using only
// bound entries that have no per-partition error. A missing or errored bound on
// one side leaves that side unconstrained.
func clampToBounds(off int64, starts, ends kadm.ListedOffsets, topic string, p int32) int64 {
	if s, ok := starts.Lookup(topic, p); ok && s.Err == nil && off < s.Offset {
		off = s.Offset
	}
	if e, ok := ends.Lookup(topic, p); ok && e.Err == nil && off > e.Offset {
		off = e.Offset
	}
	return off
}
```

Replace the discarded-error lookups at lines 136-137:

```go
	ends, _ := adm.ListEndOffsets(ctx, req.Topic)
	starts, _ := adm.ListStartOffsets(ctx, req.Topic)
```

with:

```go
	ends, endsErr := adm.ListEndOffsets(ctx, req.Topic)
	starts, startsErr := adm.ListStartOffsets(ctx, req.Topic)
	boundsErr := endsErr != nil || startsErr != nil
```

Then replace the `ResetToOffset` and `ResetShiftBy` cases (lines 162-183) so they refuse to commit unclamped when bounds are unavailable, and use the helper:

```go
		case ResetToOffset:
			newAt = req.Offset
			if s, ok := starts.Lookup(req.Topic, p); ok && s.Err == nil && newAt < s.Offset {
				newAt = s.Offset
			}
			if e, ok := ends.Lookup(req.Topic, p); ok && e.Err == nil && newAt > e.Offset {
				newAt = e.Offset
			}
		case ResetShiftBy:
			if res.OldOffset < 0 {
				res.Error = "no prior commit to shift from"
			} else {
				newAt = res.OldOffset + req.Shift
				if s, ok := starts.Lookup(req.Topic, p); ok && s.Err == nil && newAt < s.Offset {
					newAt = s.Offset
				}
				if e, ok := ends.Lookup(req.Topic, p); ok && e.Err == nil && newAt > e.Offset {
					newAt = e.Offset
				}
			}
		}
```

with:

```go
		case ResetToOffset:
			if boundsErr {
				res.Error = "offset bounds unavailable; refusing to commit an unclamped absolute offset"
			} else {
				newAt = clampToBounds(req.Offset, starts, ends, req.Topic, p)
			}
		case ResetShiftBy:
			if res.OldOffset < 0 {
				res.Error = "no prior commit to shift from"
			} else if boundsErr {
				res.Error = "offset bounds unavailable; refusing to commit an unclamped shifted offset"
			} else {
				newAt = clampToBounds(res.OldOffset+req.Shift, starts, ends, req.Topic, p)
			}
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/kafka/ -run 'TestClampToBounds' -v && go vet ./pkg/kafka/...`
Expected: PASS, no vet diagnostics. (The strategy-gating change is exercised end-to-end by the integration suite; the pure helper is covered by the unit test above.)

- [ ] **Step 5: Commit**

```bash
git add pkg/kafka/admin_groups.go pkg/kafka/admin_groups_test.go
git commit -m "fix: refuse unclamped offset reset when offset bounds are unavailable"
```

---

### Task 6: Brittle 404 detection in SubjectConfig (Medium)

**Problem:** `SubjectConfig` decides "subject has no override, fall back to global" by `strings.Contains(err.Error(), "40401")` (`pkg/kafka/schema_registry.go:186`). An unrelated 5xx whose body contains that digit run, or a subject name containing it, spuriously triggers the fallback and masks the real error. Fix: have `do` return a typed error carrying the SR error code / HTTP status, and classify with `errors.As`.

**Files:**
- Modify: `pkg/kafka/schema_registry.go` (add `SRError` type; return it from `do`; use `errors.As` in `SubjectConfig`)
- Test: `pkg/kafka/schema_registry_test.go` (uses the existing httptest mux pattern)

**Interfaces:**
- Consumes: `do(ctx, method, path, body, out) error`.
- Produces: `type SRError struct { Status int; Code int; Message string }` with `func (e *SRError) Error() string`. `do` returns `*SRError` for any `res.StatusCode >= 400`. `SubjectConfig` falls back to `/config` only when `errors.As(err, &srErr)` and (`srErr.Code == 40401` || `srErr.Status == http.StatusNotFound`).

- [ ] **Step 1: Write the failing test**

Append to `pkg/kafka/schema_registry_test.go` (created in the security plan Task 3; if running this plan independently, create the file with the standard header + imports `context`, `errors`, `net/http`, `net/http/httptest`, `testing`, testify, and `config`):

```go
func TestSubjectConfig_FallsBackToGlobalOnlyOnRealNotFound(t *testing.T) {
	t.Parallel()

	t.Run("subject_404_falls_back_to_global", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/config/orders", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error_code":40401,"message":"Subject not found."}`))
		})
		mux.HandleFunc("/config", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"compatibilityLevel":"BACKWARD"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL})

		cfg, err := c.SubjectConfig(context.Background(), "orders")

		require.NoError(t, err)
		assert.Equal(t, "BACKWARD", cfg.CompatibilityLevel)
	})

	t.Run("server_error_containing_40401_is_not_swallowed", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/config/x40401y", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error_code":50001,"message":"internal error ref 40401"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL})

		_, err := c.SubjectConfig(context.Background(), "x40401y")

		require.Error(t, err, "a 500 mentioning 40401 must NOT be treated as not-found")
		assert.Contains(t, err.Error(), "internal error")
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/kafka/ -run TestSubjectConfig_FallsBackToGlobalOnlyOnRealNotFound -v`
Expected: `server_error_containing_40401_is_not_swallowed` FAILS — the substring match treats the 500 as a not-found and tries the (non-registered) `/config` route, masking the real error.

- [ ] **Step 3: Add the typed error and classify with errors.As**

In `pkg/kafka/schema_registry.go`, add the type near `ErrNoSchemaRegistry` (after line 25):

```go
// SRError is a typed Schema Registry error carrying the HTTP status and the
// Confluent error_code so callers can classify failures without string-matching.
type SRError struct {
	Status  int
	Code    int
	Message string
}

func (e *SRError) Error() string {
	return fmt.Sprintf("sr %d: %s", e.Status, e.Message)
}
```

Replace the error branch in `do` (lines 128-139):

```go
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		// SR errors look like {"error_code":40401,"message":"..."}
		var srErr struct {
			Code    int    `json:"error_code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &srErr); err == nil && srErr.Message != "" {
			return fmt.Errorf("sr %d: %s", res.StatusCode, srErr.Message)
		}
		return fmt.Errorf("sr %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
```

with:

```go
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		// SR errors look like {"error_code":40401,"message":"..."}
		var parsed struct {
			Code    int    `json:"error_code"`
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(data))
		code := 0
		if err := json.Unmarshal(data, &parsed); err == nil && parsed.Message != "" {
			msg = parsed.Message
			code = parsed.Code
		}
		return &SRError{Status: res.StatusCode, Code: code, Message: msg}
	}
```

Replace `SubjectConfig` (lines 182-194):

```go
func (c *SchemaRegistryClient) SubjectConfig(ctx context.Context, subject string) (*SubjectConfig, error) {
	var out SubjectConfig
	p := "/config/" + url.PathEscape(subject)
	err := c.do(ctx, http.MethodGet, p, nil, &out)
	if err != nil && strings.Contains(err.Error(), "40401") {
		// subject has no override; read global
		err = c.do(ctx, http.MethodGet, "/config", nil, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
```

with:

```go
func (c *SchemaRegistryClient) SubjectConfig(ctx context.Context, subject string) (*SubjectConfig, error) {
	var out SubjectConfig
	p := "/config/" + url.PathEscape(subject)
	err := c.do(ctx, http.MethodGet, p, nil, &out)
	var srErr *SRError
	if errors.As(err, &srErr) && (srErr.Code == 40401 || srErr.Status == http.StatusNotFound) {
		// subject has no override; read global
		err = c.do(ctx, http.MethodGet, "/config", nil, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
```

(`errors` is already imported in this file.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/kafka/ -run 'TestSubjectConfig|TestSRDecoder' -v`
Expected: PASS — both new sub-tests plus the existing SR decoder tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/kafka/schema_registry.go pkg/kafka/schema_registry_test.go
git commit -m "fix: classify schema registry 404 by typed error, not substring match"
```

---

### Task 7: Metrics reports 0 groups when group listing failed (Medium)

**Problem:** When `ListGroups` errors, `snap.Groups = len(groups)` records `0` as an authoritative measured value (`pkg/kafka/metrics.go:409`), and `applyClusterAggregates` always sets `info.Groups = &g` (`:627`). The UI then shows "0 groups" for a cluster whose group listing actually failed. Fix: track whether groups are known (mirroring the existing `Have*` pattern) and leave `info.Groups` nil when unknown — the frontend already renders nil as "—".

**Files:**
- Modify: `pkg/kafka/metrics.go` (add `HaveGroups bool` to `ClusterMetrics`; set it at `:409`; guard at `:627`)
- Test: `pkg/kafka/metrics_test.go` (seed a snapshot, assert nil vs set)

**Interfaces:**
- Consumes: `ClusterMetrics` struct, `clusterState{ snapshot ClusterMetrics }`, `metricsCollector.states map[string]*clusterState`, `(*Registry).applyClusterAggregates(info *ClusterInfo)`, `ClusterInfo.Groups *int`.
- Produces: `ClusterMetrics.HaveGroups bool`. `applyClusterAggregates` sets `info.Groups` only when `snap.HaveGroups` is true.

- [ ] **Step 1: Write the failing test**

Add to `pkg/kafka/metrics_test.go` (follow the existing seeding pattern at lines 253-271, which constructs a `metricsCollector` and seeds `mc.states[...]`):

```go
func TestApplyClusterAggregates_LeavesGroupsNil_WhenGroupsUnknown(t *testing.T) {
	t.Parallel()

	r := NewRegistry(nil, slog.Default())
	r.StartMetrics(context.Background(), time.Hour) // collector with a long interval; we seed manually
	defer r.Close()

	r.metrics.statesMu.Lock()
	r.metrics.states["c"] = &clusterState{
		snapshot:    ClusterMetrics{Brokers: 3, Topics: 5, Groups: 0, HaveGroups: false},
		snapshotSet: true,
	}
	r.metrics.statesMu.Unlock()

	info := &ClusterInfo{Name: "c"}
	r.applyClusterAggregates(info)

	assert.Nil(t, info.Groups, "groups must be nil (unknown), not a measured 0, when group listing failed")
	require.NotNil(t, info.Brokers)
	assert.Equal(t, 3, *info.Brokers, "known fields still populate")
}

func TestApplyClusterAggregates_SetsGroups_WhenKnown(t *testing.T) {
	t.Parallel()

	r := NewRegistry(nil, slog.Default())
	r.StartMetrics(context.Background(), time.Hour)
	defer r.Close()

	r.metrics.statesMu.Lock()
	r.metrics.states["c"] = &clusterState{
		snapshot:    ClusterMetrics{Groups: 7, HaveGroups: true},
		snapshotSet: true,
	}
	r.metrics.statesMu.Unlock()

	info := &ClusterInfo{Name: "c"}
	r.applyClusterAggregates(info)

	require.NotNil(t, info.Groups)
	assert.Equal(t, 7, *info.Groups)
}
```

> Before writing, confirm the field name used by `ClusterMetricsSnapshot`/`clusterState` for "a snapshot is present". The existing test at `metrics_test.go:253-271` shows the exact field names (`clusterState.snapshot`, and the flag the snapshot getter checks). If the presence flag is named differently than `snapshotSet`, use that name verbatim in both the test and any reference. Run `grep -n "snapshot" pkg/kafka/metrics.go` to confirm before Step 2.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/kafka/ -run 'TestApplyClusterAggregates' -v`
Expected: FAIL to compile — `ClusterMetrics` has no `HaveGroups` field yet.

- [ ] **Step 3: Add HaveGroups and guard the assignment**

In `pkg/kafka/metrics.go`, add the field to the `ClusterMetrics` struct (alongside `HaveLag` at line 58):

```go
	HaveLag   bool
```

becomes:

```go
	HaveLag    bool
	HaveGroups bool
```

(Re-gofmt the struct so field alignment is preserved — `gofmt -w pkg/kafka/metrics.go`.)

Replace line 409:

```go
	snap.Groups = len(groups)
```

with:

```go
	if groupsErr == nil {
		snap.Groups = len(groups)
		snap.HaveGroups = true
	}
```

Replace the `info.Groups` assignment block at lines 623-627:

```go
	g := snap.Groups
	tm := snap.TotalMessages
	info.Brokers = &b
	info.Topics = &t
	info.Groups = &g
	info.TotalMessages = &tm
```

with:

```go
	tm := snap.TotalMessages
	info.Brokers = &b
	info.Topics = &t
	if snap.HaveGroups {
		g := snap.Groups
		info.Groups = &g
	}
	info.TotalMessages = &tm
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/kafka/ -run 'TestApplyClusterAggregates|TestClusterMetricsSnapshot|TestEnsureFresh' -v && gofmt -l pkg/kafka/metrics.go`
Expected: tests PASS; `gofmt -l` prints nothing (file is formatted).

- [ ] **Step 5: Commit**

```bash
git add pkg/kafka/metrics.go pkg/kafka/metrics_test.go
git commit -m "fix: report unknown group count as nil instead of a measured zero"
```

---

### Final verification (after all tasks)

- [ ] **Run the full backend suite, race detector, and linter**

Run: `go test ./... && go test -race ./internal/server/... ./pkg/kafka/... && golangci-lint run`
Expected: all green. If `make test && make lint` is the canonical entrypoint, run that instead.

- [ ] **Confirm no new dependencies were added**

Run: `git diff main -- go.mod go.sum`
Expected: empty diff.

---

## Out of scope (recorded, no action in this plan)

- **`auth.deny` JSON via concatenation** — fixed in the security plan (Task 2).
- **goja per-message runtime/timer churn** — fixed in the security plan (Task 5).
- **Low-severity backend items** — startup goroutine exit code (`server.go`), masking silent-skip (`masking.go`), and the `rbacMiddleware` unbounded body read (`rbac.go:48-64`, partially mitigated by the downstream `MaxBytesReader`) are deferred per the chosen High+Medium scope. The `rbac.go` body read is the strongest candidate for a follow-up: wrap `io.ReadAll(r.Body)` in `http.MaxBytesReader(w, r.Body, 1<<20)`.

## Notes for the implementer

- Tasks are independent; the High items (1, 2, 3) should ship first.
- Tasks 4 and 6 both touch `pkg/kafka/schema_registry_test.go` / SR client code; if you run them out of order, just ensure the file's header + imports exist once.
- Do not batch commits across tasks — each task ends green on its own.
</content>
