# Test Discipline

This document defines the testing standard every contributor — human or
agent — applies to kafkito. It is the reference that `test-quality-reviewer`
and the `TaskCompleted` hook enforce. Pair it with
[`DESIGN_GUIDELINES.md`](./DESIGN_GUIDELINES.md) (frontend) and
[`CLAUDE.md`](../CLAUDE.md) (general).

## Definition of Done — every test-touching task

A task is **done** only when all of the following hold:

1. At least one new or refactored test was **red before the production
   change** (Red → Green → Refactor demonstrated). Retro-TDD requires an
   explicit one-line justification in the commit body, approved by the
   reviewer.
2. The test asserts **observable behaviour**, not implementation detail.
3. All gates green:
   - Backend: `go test ./... && golangci-lint run` (or `make test && make lint`).
   - Frontend unit: `cd frontend && bun run lint && bun run build && bun run check:palette && bun run check:strings && bun run check:tokens && bun run check:routes && bun run check:dates && bun run test`.
   - E2E: hermetic local stack up (see commit `cebaf1f`), then `bun run e2e`.
4. No new runtime dependency without a PR-body note (`CLAUDE.md`).
5. No emojis, no German strings, no raw `Date` formatters
   (`check:dates`, `check:strings` enforce this).
6. For UI changes: light **and** dark mode spot-checked against a running
   dev server.
7. The reviewer mailbox returned `pass`.

## Test-Smell Catalog

The reviewer rejects any test that exhibits the smells below. Each smell
is named so commit messages and PR comments can reference it directly
("rejecting for `mystery-guest`").

### 1. Classic xUnit smells (Meszaros)

- **Eager Test** — multiple unrelated assertions in one `it`/`t.Run`.
  Split into focused tests.
- **Mystery Guest** — test depends on data hidden in a fixture file or
  global without naming the contract. Inline the data or use a builder.
- **Lazy Test** — only `expect(x).toBeTruthy()` / `assert.NotNil(x)` —
  no semantic assertion. Assert the value, not its existence.
- **Conditional Test Logic** — `if`, `switch`, or `for` deciding what to
  assert. Use table-driven tests with one assertion per row.
- **Fragile Test** — asserts on private fields, render order, or
  internal state. Assert through the public surface.
- **Erratic / Flaky Test** — depends on real wall-clock, real network,
  or test order. Use `vi.useFakeTimers()` / `vi.setSystemTime()` /
  fixed seeds. Pair `useFakeTimers()` with `useRealTimers()` in
  `afterEach`.
- **Slow Test** — hits a real broker / real HTTP server when an
  in-process fake (`kfake`) suffices.
- **Test Code Duplication** — copy-pasted setup. Extract a builder or
  `beforeEach`. Never extract control flow.

### 2. Mocking smells

- **Mock-Heavy** — every collaborator is a mock; the test asserts mock
  calls instead of behaviour. Replace with a real or fake collaborator.
- **Mock Leak** — production code branches on `if testing.Testing()` or
  `process.env.NODE_ENV === 'test'`. Production must not know about the
  test harness.
- **Stubbed-too-deep** — stubbing a transitive dependency three layers
  removed. Refactor the seam closer to the unit under test.
- **Interaction-only Verification** — `expect(spy).toHaveBeenCalledWith(...)`
  with no behavioural assertion afterwards. Verify the resulting state
  unless the call itself is the externally visible behaviour.

### 3. Structural smells

- **Generic Names** — `Test1`, `TestFoo`, `it('works')`. Names describe
  the behaviour: `TestProduceSync_ReturnsContextDeadlineExceeded_WhenCtxCancelled`.
- **No AAA / Arrange-Act-Assert** — collapsed setup, action, and
  assertion into one paragraph. Separate visually with blank lines.
- **Magic Literals** — un-named numbers and timestamps in assertions.
  Use named constants with semantic meaning
  (`const businessHourOpen = 9`).

### 4. State smells

- **Shared Mutable State** — package-level vars that multiple tests
  mutate. Use `t.Cleanup`, `vi.restoreAllMocks`, or fresh fixtures.
- **Order Dependence** — test B fails when run before test A. All tests
  must pass under `go test -shuffle=on` and Vitest's randomized order.
- **Committed Skips** — `t.Skip(...)`, `it.skip`, `xit`, `test.skip`,
  `it.todo` without a tracking issue. Skips become tech debt; lint-fail
  if any reach `main` without an `issue:#NNN` comment.

### 5. Go-specific smells

- **Missing `t.Parallel()`** — leaf tests that touch no shared state
  must opt into parallelism.
- **Missing `t.Cleanup`** — manual `defer` for teardown when a
  `t.Cleanup` is cleaner.
- **`panic` over `t.Fatalf`** — setup helpers that `panic` instead of
  failing the test reporting line.
- **Hand-rolled assertion ladders** — long `if got != want { t.Errorf… }`
  blocks where `assert.Equal(t, want, got)` (or `require` for
  prerequisites) is shorter and more readable.
- **`interface{}` / `any` test helpers** — typed helpers compile-check
  the test contract.
- **Real broker for unit work** — use `kfake.NewCluster(...)` for unit
  tests; reserve real Kafka for the integration tier (build-tagged with
  `//go:build integration`).
- **No `-race` coverage** — concurrent code paths must have at least
  one test that the CI runs under `go test -race`.

### 6. Vitest 4 + React Testing Library 16 smells

- **CSS-class queries** — `container.querySelector('.btn-primary')`.
  Use `screen.getByRole('button', { name: /save/i })`.
- **`fireEvent` over `userEvent`** — `userEvent` (v14+) simulates the
  full event sequence (focus, key, click); `fireEvent` skips them.
- **Snapshot without semantic assertion** — `toMatchSnapshot()` as the
  only check. Snapshots may complement, never replace, behavioural
  asserts.
- **Ignored `act` warnings** — every `act()` warning is a real async
  bug. Wrap state updates with `await userEvent.…` or use
  `await screen.findBy…`.
- **`waitFor` chains where `findBy` suffices** — `findBy*` is the
  async-aware query and is preferred over `waitFor(() => getBy*)`.
- **`useFakeTimers` without restore** — every `vi.useFakeTimers()` is
  paired with `vi.useRealTimers()` in `afterEach`. Forgetting bleeds
  fake time into the next test.
- **Unscoped `screen` in nested DOMs** — when multiple nodes match,
  scope with `within(parent).getByRole(...)`.

### 7. Playwright 1.59 smells

- **CSS-selector locators** — `page.locator('.hero__title')`. Use
  `page.getByRole`, `getByLabel`, `getByText`, then `getByTestId` as
  the last resort. Locator priority is part of the public Playwright
  best-practice guide.
- **`waitForTimeout(ms)`** — magic-number sleeps. Use web-first
  assertions (`await expect(locator).toBeVisible()`); they auto-retry.
- **Manual sleep + click** — `await page.click(...); await sleep(500)`.
  Replace with `await expect(targetLocator).toBeVisible()`.
- **`expect(x).toBeTruthy()` over web-first** — use
  `await expect(locator).toBeVisible() / toBeEnabled() / toHaveText(...)`
  so retries kick in.
- **Cross-spec state pollution** — relying on the previous spec's
  cluster / topic. Each spec builds its own state in `beforeEach`,
  cleans up in `afterEach`.
- **No fixture for shared setup** — copy-pasting login / cluster-pick
  across specs. Extract into a Playwright fixture.

### 8. Production-code spillover smells

When a test forces a production change, the change must improve the
production code on its own merits. Reject these patterns:

- **Test-only abstractions** — interfaces extracted "for mocking" that
  add nothing for production callers.
- **DI-leakage** — constructors that now take a clock / random / fs
  parameter that production wires from a global. Use a real default and
  override in test.
- **Unjustified `interface{}` returns** — broadening a typed return so
  a test fake fits.

## Per-stack idioms

### Backend (Go + testify + franz-go)

- Combine **table-driven tests** with `assert` (non-fatal) and
  `require` (prerequisite). Idiomatic skeleton:

  ```go
  for _, tt := range tests {
      tt := tt
      t.Run(tt.name, func(t *testing.T) {
          t.Parallel()
          got, err := MyFunc(tt.input)
          if tt.wantErr != "" {
              require.Error(t, err)
              assert.ErrorContains(t, err, tt.wantErr)
              return
          }
          require.NoError(t, err)
          assert.Equal(t, tt.want, got)
      })
  }
  ```

- Mocking interfaces: `testify/mock` with explicit `On(...).Return(...)`.
  Unset expectations or use the `mock.AnythingOfType` matcher; never
  `mock.Anything` unless the value is irrelevant.
- Suite-style only when shared per-test setup is real (DB connection,
  cluster). Otherwise keep tests free-standing.
- For Kafka: use `kfake.NewCluster` for unit / integration-light tests
  — it runs in-process, no Docker, near-instant. Reserve the
  Compose-backed cluster for the e2e tier and a small integration
  smoke. Intercept protocol calls with `cluster.ControlKey(...)`.
- Always run `go test -race` in CI for any package touching
  goroutines, channels, or the `kgo.Client`.

### Frontend unit (Vitest 4 + RTL 16 + happy-dom)

- Query priority: `getByRole` → `getByLabelText` → `getByPlaceholderText`
  → `getByText` → `getByDisplayValue` → `getByAltText` → `getByTitle`
  → `getByTestId` (last resort, only when no semantic option exists).
- Async: prefer `await screen.findByRole(...)` over
  `waitFor(() => screen.getByRole(...))`.
- Negative assertion: `expect(screen.queryByRole(...)).toBeNull()` — use
  `queryBy*` (not `getBy*`) for absence.
- User input: `await userEvent.click(...)` / `userEvent.type(...)`.
  Setup pattern: `const user = userEvent.setup()` once per test.
- Time-sensitive logic:

  ```ts
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('opens after 2h', () => {
    const t0 = new Date(2026, 0, 1, 9)
    vi.setSystemTime(t0)
    // act, advance, assert
  })
  ```

- Mocks: `vi.fn()` for new mocks, `vi.spyOn(obj, 'method')` for tracking
  existing functions. Restore with `vi.restoreAllMocks()` in `afterEach`.

### E2E (Playwright 1.59)

- Fixtures, not copy-paste: extend `test` with project-specific
  fixtures (logged-in cluster, seeded topic, ...).
- `test.beforeEach` for state setup, `test.afterEach` for cleanup.
- Keep `fullyParallel: false` and `workers: 1` (current config) for
  Kafka-state isolation; revisit only after we have per-test
  cluster-namespace isolation.
- Web-first assertions everywhere. The `delete-records` and
  `reset-offsets` specs are the templates.
- Trace + screenshot + video on failure are already configured —
  attach them to PR descriptions when a flake reproduces.
- Do not use the `chromium` project name as a free dimension; the
  config currently runs only Chromium and that is intentional until
  we have a cross-browser story.

## Reviewer Checklist (used by `test-quality-reviewer`)

For every `review-request` from a domain teammate, verify in order:

1. **Scope** — only files in this teammate's domain were touched
   (`backend-tdd` → `pkg/`, `internal/`, `cmd/`, `proto/`, `*_test.go`;
   `frontend-unit-tdd` → `frontend/src/**`; `e2e-tdd` → `frontend/e2e/`,
   `frontend/playwright.config.ts`, `docker-compose.*`). Cross-domain
   changes require an explicit Lead-approved exception.
2. **Phase-1 rule** — if the task is labelled Phase 1, no production
   files were touched.
3. **Red-Green-Refactor** — commit history (or task description) shows
   the test was added in a state that fails against pre-existing
   production code.
4. **Smell scan** — every changed test passes through Sections 1–8
   above. Cite the smell name on rejection.
5. **Idiom adherence** — Section "Per-stack idioms".
6. **Gate evidence** — the teammate's `review-request` cites the gate
   command output. Reviewer re-runs in read-only mode if the evidence
   is stale (> 10 min) or missing.
7. **Context7 evidence** — for any non-trivial library API used, the
   teammate cites the Context7 doc page they consulted. Trust-but-verify
   on dispute.

Reply format:

```
pass: <one-line summary>
```

or

```
fail: <smell-name-or-rule>: <one-line reason>
suggestion: <one-line concrete fix>
```

The teammate must address every `fail` before re-requesting review.
