# Frontend Bug Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the High- and Medium-severity frontend correctness bugs from the 2026-06-23 audit (CSRF/auth request races, the message "load more" filter race, confirm-dialog closing before its mutation, dropdown/keyboard bugs, duplicate React keys, stale component state, and cross-tab state staleness).

**Architecture:** Each bug is an independent task ending in a runnable test and a commit. Logic that currently lives inside large route components is extracted into small pure helpers in `lib/` so it can be unit-tested without rendering an entire route; component-level behavior is tested with React Testing Library; the one genuinely timing-coupled bug (load-more race) is verified with an e2e step plus a unit-tested generation guard.

**Tech Stack:** React 18 + TanStack Query + TanStack Router, Vitest 4, React Testing Library 16, happy-dom. Tests are co-located `*.test.ts(x)` next to the unit under test, following the existing pattern in `frontend/src/auth/api.test.ts` (`vi.mock`, `vi.stubGlobal`, `vi.mocked`).

## Global Constraints

- **Hard gate (from CLAUDE.md):** before finishing ANY turn that touched `frontend/`, all of these MUST pass: `cd frontend && bun run lint && bun run build && bun run check:palette && bun run check:strings && bun run check:tokens && bun run check:routes && bun run check:dates && bun run test`. Run them at the end of every task before committing.
- Read `docs/DESIGN_GUIDELINES.md` before touching components; use `@theme` token utilities (`bg-panel`, `text-muted`, `border-border`), never default Tailwind palette classes. Do not introduce new `[var(--color-…)]` arbitrary classes (the token-sweep is a separate plan).
- UI strings and comments are English only. No emojis in UI chrome.
- No new runtime dependencies without explicit approval in the PR description.
- When a fix references a backend field that does not exist, render `"—"` and leave a `// TODO(backend): …` comment (not expected in this plan — these are pure frontend bugs).

---

### Task 1: De-duplicate the in-flight CSRF token fetch (High)

**Problem:** `getCsrfToken` (`frontend/src/auth/csrf.ts`) has no in-flight dedup. Concurrent writes with an empty cache each fire their own `HEAD /` request; if the backend rotates the token per fetch, a later sibling supersedes the token an earlier write captured, causing spurious 403s under concurrency. Fix: cache the in-flight promise and return it to concurrent callers.

**Files:**
- Modify: `frontend/src/auth/csrf.ts`
- Test: `frontend/src/auth/csrf.test.ts` (create)

**Interfaces:**
- Consumes: `fetch` (global), the response header `x-csrf-token`.
- Produces: `getCsrfToken(): Promise<string>` (unchanged signature) — concurrent calls before the first resolves share one fetch; `clearCsrfToken(): void` also clears the in-flight promise.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/auth/csrf.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearCsrfToken, getCsrfToken } from "./csrf";

beforeEach(() => {
  clearCsrfToken();
  vi.stubGlobal("fetch", vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearCsrfToken();
});

describe("getCsrfToken", () => {
  it("dedupes concurrent calls into a single fetch", async () => {
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock.mockResolvedValue(
      new Response(null, { status: 200, headers: { "x-csrf-token": "tok-1" } }),
    );

    const [a, b, c] = await Promise.all([
      getCsrfToken(),
      getCsrfToken(),
      getCsrfToken(),
    ]);

    expect(a).toBe("tok-1");
    expect(b).toBe("tok-1");
    expect(c).toBe("tok-1");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("re-fetches after clearCsrfToken", async () => {
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock
      .mockResolvedValueOnce(
        new Response(null, { status: 200, headers: { "x-csrf-token": "tok-1" } }),
      )
      .mockResolvedValueOnce(
        new Response(null, { status: 200, headers: { "x-csrf-token": "tok-2" } }),
      );

    expect(await getCsrfToken()).toBe("tok-1");
    clearCsrfToken();
    expect(await getCsrfToken()).toBe("tok-2");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && bun run test src/auth/csrf.test.ts`
Expected: the dedup test FAILS — `fetch` is called 3 times.

- [ ] **Step 3: Add in-flight promise caching**

Replace the whole body of `frontend/src/auth/csrf.ts` below the header comment:

```ts
let cachedToken: string | null = null;

export async function getCsrfToken(): Promise<string> {
  if (cachedToken) return cachedToken;
  const res = await fetch('/', {
    method: 'HEAD',
    credentials: 'include',
    headers: { 'x-csrf-token': 'fetch' },
  });
  const tok = res.headers.get('x-csrf-token');
  if (!tok) throw new Error('no csrf token in response');
  cachedToken = tok;
  return tok;
}

export function clearCsrfToken(): void {
  cachedToken = null;
}
```

with:

```ts
let cachedToken: string | null = null;
let inFlight: Promise<string> | null = null;

export async function getCsrfToken(): Promise<string> {
  if (cachedToken) return cachedToken;
  if (inFlight) return inFlight;
  inFlight = (async () => {
    const res = await fetch('/', {
      method: 'HEAD',
      credentials: 'include',
      headers: { 'x-csrf-token': 'fetch' },
    });
    const tok = res.headers.get('x-csrf-token');
    if (!tok) throw new Error('no csrf token in response');
    cachedToken = tok;
    return tok;
  })();
  try {
    return await inFlight;
  } finally {
    inFlight = null;
  }
}

export function clearCsrfToken(): void {
  cachedToken = null;
  inFlight = null;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && bun run test src/auth/csrf.test.ts`
Expected: PASS.

- [ ] **Step 5: Run the frontend hard gate and commit**

Run: `cd frontend && bun run lint && bun run build && bun run check:palette && bun run check:strings && bun run check:tokens && bun run check:routes && bun run check:dates && bun run test`
Expected: all PASS.

```bash
git add frontend/src/auth/csrf.ts frontend/src/auth/csrf.test.ts
git commit -m "fix(frontend): dedupe in-flight CSRF token fetch"
```

---

### Task 2: Bound the 403 CSRF retry to a single attempt (High)

**Problem:** `apiFetch`'s 403 retry (`frontend/src/auth/api.ts`) calls `apiFetch(input, init)` again with no recursion guard. A persistently-403 backend (or a silently-failing token fetch) recurses forever → request flood / stack growth. Fix: thread a private `retried` flag so the retry happens at most once.

**Files:**
- Modify: `frontend/src/auth/api.ts`
- Test: `frontend/src/auth/api.test.ts` (add a persistent-403 test)

**Interfaces:**
- Consumes: `getCsrfToken`, `clearCsrfToken`, `fetch`.
- Produces: `apiFetch(input: RequestInfo, init?: RequestInit): Promise<Response>` — public signature unchanged; an internal third parameter `retried = false` guards the single retry.

- [ ] **Step 1: Write the failing test**

Add to `frontend/src/auth/api.test.ts` inside the `describe("apiFetch — 403 CSRF retry semantics", ...)` block:

```ts
  it("retries at most once on a persistent 403 (no infinite recursion)", async () => {
    mockedGetCsrfToken.mockResolvedValue("tok");
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    // Always answer 403 Required — a naive retry would recurse forever.
    fetchMock.mockResolvedValue(
      makeResponse(403, { "x-csrf-token": "Required" }),
    );

    const res = await apiFetch("/x", { method: "POST" });

    expect(res.status).toBe(403);
    expect(fetchMock).toHaveBeenCalledTimes(2); // original + exactly one retry
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && bun run test src/auth/api.test.ts`
Expected: the new test FAILS — the call recurses (the test runner will hang or blow the call count well past 2; if it hangs, that itself confirms the bug).

- [ ] **Step 3: Add the recursion guard**

In `frontend/src/auth/api.ts`, change the signature:

```ts
export async function apiFetch(input: RequestInfo, init: RequestInit = {}): Promise<Response> {
```

to:

```ts
export async function apiFetch(input: RequestInfo, init: RequestInit = {}, retried = false): Promise<Response> {
```

Then change the 403 branch:

```ts
  if (res.status === 403 && res.headers.get('x-csrf-token') === 'Required') {
    clearCsrfToken();
    return apiFetch(input, init);
  }
```

to:

```ts
  if (res.status === 403 && res.headers.get('x-csrf-token') === 'Required' && !retried) {
    clearCsrfToken();
    return apiFetch(input, init, true);
  }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && bun run test src/auth/api.test.ts`
Expected: PASS (including the existing single-retry success test, which still retries once because its first call sets `retried=true` on the second).

- [ ] **Step 5: Run the frontend hard gate and commit**

Run: `cd frontend && bun run lint && bun run build && bun run check:palette && bun run check:strings && bun run check:tokens && bun run check:routes && bun run check:dates && bun run test`

```bash
git add frontend/src/auth/api.ts frontend/src/auth/api.test.ts
git commit -m "fix(frontend): guard CSRF 403 retry against infinite recursion"
```

---

### Task 3: De-duplicate the 401 auth-loss redirect (Medium)

**Problem:** On session expiry, the parallel `currentUser` and `me` queries (`frontend/src/auth/AuthProvider.tsx`) both 401, so `apiFetch` calls `window.location.assign('/')` twice and throws two `SessionExpiredError`s, racing the OAuth redirect. Fix: a module-level `redirecting` flag so the first 401 navigates and later ones only throw.

**Files:**
- Modify: `frontend/src/auth/api.ts`
- Test: `frontend/src/auth/api.test.ts`

**Interfaces:**
- Consumes: `window.location.assign`, `clearCsrfToken`.
- Produces: a module-private `redirecting` flag; `apiFetch` still throws `SessionExpiredError` on every 401 but calls `window.location.assign('/')` at most once per page load.

- [ ] **Step 1: Write the failing test**

Add to `frontend/src/auth/api.test.ts` inside `describe("apiFetch — 401 auth-loss redirect", ...)`:

```ts
  it("navigates to '/' only once across multiple concurrent 401s", async () => {
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock.mockResolvedValue(makeResponse(401));

    const results = await Promise.allSettled([
      apiFetch("/a"),
      apiFetch("/b"),
      apiFetch("/c"),
    ]);

    // Every call still rejects with SessionExpiredError…
    for (const r of results) {
      expect(r.status).toBe("rejected");
      expect((r as PromiseRejectedResult).reason).toBeInstanceOf(SessionExpiredError);
    }
    // …but the top-window navigation happens at most once.
    expect(window.location.assign).toHaveBeenCalledTimes(1);
  });
```

> Note: because `redirecting` is module state, this test must run with a fresh module or reset the flag. Add an exported test-only reset: `export function __resetRedirectForTests() { redirecting = false; }` and call it in this test's first line. (The existing single-401 test sets the flag; resetting keeps tests order-independent.) Also call `__resetRedirectForTests()` at the top of the existing "on 401 clears the CSRF cache…" test.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && bun run test src/auth/api.test.ts`
Expected: FAIL — `window.location.assign` is called 3 times (and `__resetRedirectForTests` is undefined until Step 3).

- [ ] **Step 3: Add the redirect guard**

In `frontend/src/auth/api.ts`, add a module-level flag near the top (after the `writeMethods` set):

```ts
let redirecting = false;

// Test-only: reset the one-shot redirect guard between cases.
export function __resetRedirectForTests(): void {
  redirecting = false;
}
```

Change the 401 branch:

```ts
  if (res.status === 401) {
    clearCsrfToken();
    window.location.assign('/');
    throw new SessionExpiredError();
  }
```

to:

```ts
  if (res.status === 401) {
    clearCsrfToken();
    if (!redirecting) {
      redirecting = true;
      window.location.assign('/');
    }
    throw new SessionExpiredError();
  }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && bun run test src/auth/api.test.ts`
Expected: PASS.

- [ ] **Step 5: Run the frontend hard gate and commit**

Run the full hard-gate command, then:

```bash
git add frontend/src/auth/api.ts frontend/src/auth/api.test.ts
git commit -m "fix(frontend): navigate to '/' only once on concurrent 401s"
```

---

### Task 4: ConfirmDialog must stay open until its async action settles (High)

**Problem:** `ConfirmDialog.handleConfirm` (`frontend/src/components/confirm-dialog.tsx`) does `await onConfirm(); onOpenChange(false);`. The reset-offsets modal passes `onConfirm={() => { setErr(null); commitMut.mutate(); }}` (`frontend/src/components/reset-offsets-modal.tsx:202-205`) — `mutate()` returns `void`, so the `await` resolves instantly, the dialog closes before the commit settles, and an error renders behind a closed dialog. Fix: (a) ConfirmDialog keeps the dialog open if `onConfirm` rejects; (b) reset-offsets uses `mutateAsync` so the dialog's `busy` state and error handling cover the request.

**Files:**
- Modify: `frontend/src/components/confirm-dialog.tsx:56-66` (catch a rejecting `onConfirm`)
- Modify: `frontend/src/components/reset-offsets-modal.tsx:202-205` (use `mutateAsync`)
- Test: `frontend/src/components/confirm-dialog.test.tsx` (create)

**Interfaces:**
- Consumes: `onConfirm: () => void | Promise<void>`, `onOpenChange: (open: boolean) => void`.
- Produces: `handleConfirm` closes the dialog only when `onConfirm` resolves; on rejection it stays open and clears `busy`. `reset-offsets-modal` passes `onConfirm={async () => { setErr(null); await commitMut.mutateAsync(); }}`.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/components/confirm-dialog.test.tsx`:

```tsx
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfirmDialog } from "./confirm-dialog";

afterEach(cleanup);

describe("ConfirmDialog", () => {
  it("stays open when onConfirm rejects", async () => {
    const onOpenChange = vi.fn();
    const onConfirm = vi.fn().mockRejectedValue(new Error("commit failed"));

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Commit new offsets?"
        confirmLabel="Commit reset"
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Commit reset" }));

    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("closes when onConfirm resolves", async () => {
    const onOpenChange = vi.fn();
    const onConfirm = vi.fn().mockResolvedValue(undefined);

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Commit new offsets?"
        confirmLabel="Commit reset"
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Commit reset" }));

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });
});
```

> If `ConfirmDialog` requires props beyond those shown (e.g. a mandatory `description` or `confirmPhrase`), read its prop type at the top of `confirm-dialog.tsx` and add only the required ones. `confirmPhrase` must be omitted here so the confirm button is enabled without typing.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && bun run test src/components/confirm-dialog.test.tsx`
Expected: "stays open when onConfirm rejects" FAILS — the unhandled rejection currently surfaces and/or `onOpenChange(false)` semantics are wrong.

- [ ] **Step 3: Catch a rejecting onConfirm in ConfirmDialog**

In `frontend/src/components/confirm-dialog.tsx`, replace `handleConfirm` (lines 56-66):

```tsx
  const handleConfirm = async () => {
    if (busy || !phraseOk) return;
    setBusy(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } finally {
      setBusy(false);
    }
  };
```

with:

```tsx
  const handleConfirm = async () => {
    if (busy || !phraseOk) return;
    setBusy(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } catch {
      // Keep the dialog open so the caller can surface the error inline.
    } finally {
      setBusy(false);
    }
  };
```

- [ ] **Step 4: Make reset-offsets await the mutation**

In `frontend/src/components/reset-offsets-modal.tsx`, replace the `onConfirm` (lines 202-205):

```tsx
            onConfirm={() => {
              setErr(null);
              commitMut.mutate();
            }}
```

with:

```tsx
            onConfirm={async () => {
              setErr(null);
              await commitMut.mutateAsync();
            }}
```

(`commitMut` is a TanStack `useMutation`; `mutateAsync` already exists and its `onError` still runs to set `err`. Rethrowing from `mutateAsync` keeps the dialog open via the catch added in Step 3.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd frontend && bun run test src/components/confirm-dialog.test.tsx`
Expected: PASS.

- [ ] **Step 6: Run the frontend hard gate and commit**

```bash
git add frontend/src/components/confirm-dialog.tsx frontend/src/components/reset-offsets-modal.tsx frontend/src/components/confirm-dialog.test.tsx
git commit -m "fix(frontend): keep confirm dialog open until async action settles"
```

---

### Task 5: PathSense — close on outside click and read the typed query on Tab (High + Medium)

**Problem:** (a) The PathSense combobox dropdown (`frontend/src/components/path-sense.tsx`) only closes on Escape or selection; clicking elsewhere leaves it open, overlapping other UI. (b) The `Tab` array-scope toggle reads the controlled `value` prop, which lags the local `query` within a keystroke, so it can overwrite freshly-typed text. Fix: add an outside-click/focusout handler while open; make the Tab handler operate on `query`.

**Files:**
- Modify: `frontend/src/components/path-sense.tsx`
- Test: `frontend/src/components/path-sense.test.tsx` (create)

**Interfaces:**
- Consumes: props `tree`, `value`, `onChange`, `onPick`, `placeholder`; local `open`/`query` state.
- Produces: PathSense closes when focus/click leaves its root; the Tab toggle uses `query` (the live typed value) consistently.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/components/path-sense.test.tsx`:

```tsx
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { PathSense } from "./path-sense";

afterEach(cleanup);

const tree = { kind: "object" as const, fields: [] }; // empty tree: dropdown shows the "manual entry" hint

describe("PathSense", () => {
  it("closes the dropdown when focus leaves the component", () => {
    render(
      <div>
        <PathSense tree={tree} value="" onChange={vi.fn()} onPick={vi.fn()} />
        <button type="button">outside</button>
      </div>,
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(input).toHaveAttribute("aria-expanded", "true");

    fireEvent.blur(input, { relatedTarget: screen.getByText("outside") });

    expect(input).toHaveAttribute("aria-expanded", "false");
  });

  it("Tab toggles the array segment based on the freshly typed query, not the lagging value prop", () => {
    const onChange = vi.fn();
    render(
      <PathSense tree={tree} value="a[0].b" onChange={onChange} onPick={vi.fn()} />,
    );
    const input = screen.getByRole("combobox");
    // Simulate the user typing a new array path that the parent has not yet echoed back.
    fireEvent.change(input, { target: { value: "items[2].sku" } });
    onChange.mockClear();

    fireEvent.keyDown(input, { key: "Tab" });

    // It must toggle "items[2]" -> "items[*]", derived from the typed query.
    expect(onChange).toHaveBeenCalledWith("items[*].sku");
  });
});
```

> The `tree` prop shape above (`{ kind: "object", fields: [] }`) must match the `PathTree` type PathSense expects. Read the type import at the top of `path-sense.tsx` and adjust the literal to the minimal valid empty tree if the shape differs (the goal is only that `toRows(tree)` returns an empty list so the dropdown renders the manual-entry hint).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && bun run test src/components/path-sense.test.tsx`
Expected: FAIL — there is no blur handler (aria-expanded stays "true"), and the Tab handler reads `value` (`a[0].b`) so it would emit `a[*].b`, not `items[*].sku`.

- [ ] **Step 3: Add the outside-close handler and fix the Tab source**

In `frontend/src/components/path-sense.tsx`, add a root ref and a blur handler. Change the component's local state region (after `const [query, setQuery] = useState(value);`) to add the ref import and ref:

At the top, ensure `useRef` is imported from `react`. Inside the component, add:

```tsx
  const rootRef = useRef<HTMLDivElement>(null);
```

Change the `onKey` handler's `Tab` branch from:

```tsx
    } else if (e.key === "Tab") {
      if (/\[(\*|\d+)\]/.test(value)) {
        e.preventDefault();
        onChange(toggleArraySegment(value));
      }
    }
```

to:

```tsx
    } else if (e.key === "Tab") {
      if (/\[(\*|\d+)\]/.test(query)) {
        e.preventDefault();
        onChange(toggleArraySegment(query));
      }
    }
```

Change the wrapping `<div className="relative">` to attach the ref and a focusout handler:

```tsx
  return (
    <div className="relative">
```

to:

```tsx
  return (
    <div
      ref={rootRef}
      className="relative"
      onBlur={(e) => {
        if (!rootRef.current?.contains(e.relatedTarget as Node | null)) {
          setOpen(false);
        }
      }}
    >
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && bun run test src/components/path-sense.test.tsx`
Expected: PASS.

- [ ] **Step 5: Run the frontend hard gate and commit**

```bash
git add frontend/src/components/path-sense.tsx frontend/src/components/path-sense.test.tsx
git commit -m "fix(frontend): close PathSense on outside click and toggle from typed query"
```

---

### Task 6: De-duplicate rendered messages by partition-offset (Medium)

**Problem:** The message list keys rows by `${m.partition}-${m.offset}` (`...messages.tsx:1111`). When the search accumulator or browse/tail seam returns the same record twice, React key collisions drop or misrender rows. Fix: extract a pure `dedupeMessages` helper, unit-test it, and apply it to the rendered list.

**Files:**
- Create: `frontend/src/lib/dedupe-messages.ts`
- Create: `frontend/src/lib/dedupe-messages.test.ts`
- Modify: `frontend/src/routes/clusters.$cluster.topics.$topic.messages.tsx` (apply to `displayMessages` before render)

**Interfaces:**
- Consumes: an array of message-like objects with numeric `partition` and `offset`.
- Produces: `dedupeMessages<T extends { partition: number; offset: number }>(messages: T[]): T[]` — preserves first-seen order, drops later duplicates with the same `partition`+`offset`.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/lib/dedupe-messages.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { dedupeMessages } from "./dedupe-messages";

describe("dedupeMessages", () => {
  it("drops later duplicates with the same partition+offset, preserving order", () => {
    const input = [
      { partition: 0, offset: 1, v: "a" },
      { partition: 0, offset: 2, v: "b" },
      { partition: 0, offset: 1, v: "a-dup" },
      { partition: 1, offset: 1, v: "c" },
    ];

    expect(dedupeMessages(input)).toEqual([
      { partition: 0, offset: 1, v: "a" },
      { partition: 0, offset: 2, v: "b" },
      { partition: 1, offset: 1, v: "c" },
    ]);
  });

  it("returns an empty array unchanged", () => {
    expect(dedupeMessages([])).toEqual([]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && bun run test src/lib/dedupe-messages.test.ts`
Expected: FAIL — module does not exist.

- [ ] **Step 3: Implement the helper**

Create `frontend/src/lib/dedupe-messages.ts`:

```ts
// dedupeMessages removes records sharing the same partition+offset, keeping the
// first occurrence. Used so the message list never renders colliding React keys
// when the search accumulator or browse/tail seam returns a record twice.
export function dedupeMessages<T extends { partition: number; offset: number }>(
  messages: T[],
): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const m of messages) {
    const key = `${m.partition}-${m.offset}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(m);
  }
  return out;
}
```

- [ ] **Step 4: Apply it in the route**

In `frontend/src/routes/clusters.$cluster.topics.$topic.messages.tsx`, add the import alongside the other `lib` imports:

```ts
import { dedupeMessages } from "../lib/dedupe-messages";
```

> Confirm the correct relative path: from `src/routes/` to `src/lib/` it is `../lib/dedupe-messages`. Match the style of the file's existing `lib` imports.

Find where `displayMessages` is computed (it is the array passed to `.map((m) => <MessageRow … key={`${m.partition}-${m.offset}`}/>)` at line ~1108). Wrap its definition so the value rendered is de-duplicated. Locate the `const displayMessages = …` declaration and wrap the right-hand side:

```ts
const displayMessages = dedupeMessages(/* existing expression */);
```

(If `displayMessages` is produced via `useMemo`, keep the `useMemo` and wrap its returned array with `dedupeMessages(...)` inside the memo callback. Do not change the dependency array.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd frontend && bun run test src/lib/dedupe-messages.test.ts`
Expected: PASS.

- [ ] **Step 6: Run the frontend hard gate and commit**

```bash
git add frontend/src/lib/dedupe-messages.ts frontend/src/lib/dedupe-messages.test.ts "frontend/src/routes/clusters.\$cluster.topics.\$topic.messages.tsx"
git commit -m "fix(frontend): dedupe rendered messages to avoid colliding React keys"
```

---

### Task 7: Guard the "Load more" append against a filter change (High)

**Problem:** `loadMore` (`...messages.tsx:411-428`) captures `params`/`tailCursor`; if the user changes a filter while a page is in flight, the reset effect clears `tailMessages`, but the stale in-flight promise still appends a wrong-filter page via `setTailMessages((prev) => [...prev, ...])`. Fix: a generation token (a ref bumped on every reset) so a resolved page is ignored when the generation changed mid-flight.

**Files:**
- Modify: `frontend/src/routes/clusters.$cluster.topics.$topic.messages.tsx` (add `loadGenRef`, bump on reset, compare after await)
- Verify: `frontend/e2e/` (e2e step; the race is timing-coupled and is verified end-to-end)

**Interfaces:**
- Consumes: existing `useState`/`useEffect`/`useRef` (`useRef` may need importing), `setTailMessages`, `setTailCursor`.
- Produces: a `loadGenRef` ref; `loadMore` snapshots the generation before fetching and discards the result if `loadGenRef.current` changed.

- [ ] **Step 1: Add the generation ref and bump it on reset**

In `frontend/src/routes/clusters.$cluster.topics.$topic.messages.tsx`, ensure `useRef` is imported from `react`. Near the tail-state declarations (`const [tailMessages, setTailMessages] = useState<Message[]>([]);` etc.), add:

```ts
  const loadGenRef = useRef(0);
```

Change the reset effect (lines 405-409):

```ts
  useEffect(() => {
    setTailMessages([]);
    setTailCursor(msgsQuery.data?.next_cursor);
    setLoadMoreError(null);
  }, [msgsQuery.data]);
```

to:

```ts
  useEffect(() => {
    loadGenRef.current += 1;
    setTailMessages([]);
    setTailCursor(msgsQuery.data?.next_cursor);
    setLoadMoreError(null);
  }, [msgsQuery.data]);
```

- [ ] **Step 2: Discard stale results in loadMore**

Change `loadMore` (lines 411-428):

```ts
  const loadMore = async () => {
    if (!tailCursor) return;
    setLoadingMore(true);
    setLoadMoreError(null);
    try {
      const next = await fetchMessages(cluster, topic, {
        ...params,
        cursor: tailCursor,
      });
      setTailMessages((prev) => [...prev, ...(next.messages ?? [])]);
      setTailCursor(next.has_more ? next.next_cursor : undefined);
    } catch (err) {
      setLoadMoreError((err as Error).message);
    } finally {
      setLoadingMore(false);
    }
  };
```

to:

```ts
  const loadMore = async () => {
    if (!tailCursor) return;
    const gen = loadGenRef.current;
    setLoadingMore(true);
    setLoadMoreError(null);
    try {
      const next = await fetchMessages(cluster, topic, {
        ...params,
        cursor: tailCursor,
      });
      if (loadGenRef.current !== gen) return; // filter changed mid-flight; drop this page
      setTailMessages((prev) => [...prev, ...(next.messages ?? [])]);
      setTailCursor(next.has_more ? next.next_cursor : undefined);
    } catch (err) {
      if (loadGenRef.current !== gen) return;
      setLoadMoreError((err as Error).message);
    } finally {
      if (loadGenRef.current === gen) setLoadingMore(false);
    }
  };
```

- [ ] **Step 3: Type-check and run the existing unit suite**

Run: `cd frontend && bun run build && bun run test`
Expected: build PASSES (types OK) and the unit suite stays green.

- [ ] **Step 4: Verify the race end-to-end**

The race is timing-coupled and is verified through the e2e stack (owned by the e2e-tdd agent). Add/extend a spec under `frontend/e2e/` that: (1) opens a topic's messages, (2) clicks "Load more" and immediately changes the partition filter before the response resolves, (3) asserts the rendered rows all belong to the new filter (no mixed-filter rows). Run: `cd frontend && bun run test:e2e` (or the project's e2e command). If the e2e harness is not available in this environment, record a manual verification with these exact steps in the PR body.

- [ ] **Step 5: Commit**

```bash
git add "frontend/src/routes/clusters.\$cluster.topics.\$topic.messages.tsx"
git commit -m "fix(frontend): drop stale Load-more pages when the message filter changes"
```

---

### Task 8: ArrayNode must re-derive its expanded state when data changes (Medium)

**Problem:** `ArrayNode.expanded` (`frontend/src/components/json-interactive.tsx:208-210`) is initialised once from `arr.length`. When the same React position is reused with different data (message navigation swaps a small array for a >100-item one), the collapse state does not re-derive. Fix: key the node by its content/trail so React remounts it when the data changes.

**Files:**
- Modify: `frontend/src/components/json-interactive.tsx` (add a `key` where `ArrayNode` is rendered)
- Test: covered by build + the existing json-interactive behavior; a focused remount assertion is added if a test file exists.

**Interfaces:**
- Consumes: `ArrayNode` props `arr`, `trail`; `ARRAY_COLLAPSE_THRESHOLD`.
- Produces: a stable-yet-content-sensitive `key` on the `<ArrayNode>` element so a changed `arr` (different length crossing the threshold) remounts and re-derives `expanded`.

- [ ] **Step 1: Locate where ArrayNode is rendered**

Run: `grep -n "<ArrayNode" frontend/src/components/json-interactive.tsx`
Expected: one or more JSX usages of `<ArrayNode … />`.

- [ ] **Step 2: Add a content-sensitive key**

At each `<ArrayNode … />` render site, add a `key` derived from the trail and the array length so a different dataset remounts the node:

```tsx
<ArrayNode
  key={`${trail.map((t) => String(t)).join(".")}:${arr.length}`}
  arr={arr}
  trail={trail}
  /* …existing props… */
/>
```

> Use the variable names in scope at that render site (the surrounding code passes `arr`/`trail` or equivalently-named locals). The key must include `arr.length` so an array crossing `ARRAY_COLLAPSE_THRESHOLD` remounts and recomputes `expanded`.

- [ ] **Step 3: Type-check and run the unit suite**

Run: `cd frontend && bun run build && bun run test`
Expected: build PASSES, unit suite green.

- [ ] **Step 4: Run the frontend hard gate and commit**

```bash
git add frontend/src/components/json-interactive.tsx
git commit -m "fix(frontend): remount ArrayNode on data change so collapse state re-derives"
```

---

### Task 9: CommandPalette — compute the latest schema version by max, not array order (Medium)

**Problem:** `latest: s.versions[s.versions.length - 1] ?? 1` (`frontend/src/components/CommandPalette.tsx:225`) assumes ascending order; an unsorted/descending API response yields a wrong "latest". Fix: extract a `latestVersion` helper using `Math.max`, unit-test it, and use it.

**Files:**
- Create: `frontend/src/lib/schema-version.ts`
- Create: `frontend/src/lib/schema-version.test.ts`
- Modify: `frontend/src/components/CommandPalette.tsx:225`

**Interfaces:**
- Consumes: `number[]` of schema versions.
- Produces: `latestVersion(versions: number[]): number` — the maximum version, or `1` for an empty array.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/lib/schema-version.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { latestVersion } from "./schema-version";

describe("latestVersion", () => {
  it("returns the maximum regardless of order", () => {
    expect(latestVersion([1, 2, 3])).toBe(3);
    expect(latestVersion([3, 1, 2])).toBe(3);
    expect(latestVersion([5])).toBe(5);
  });

  it("defaults to 1 for an empty list", () => {
    expect(latestVersion([])).toBe(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && bun run test src/lib/schema-version.test.ts`
Expected: FAIL — module does not exist.

- [ ] **Step 3: Implement the helper and use it**

Create `frontend/src/lib/schema-version.ts`:

```ts
// latestVersion returns the highest schema version, independent of the order
// the registry returned them in. Defaults to 1 when no versions are known.
export function latestVersion(versions: number[]): number {
  return versions.length > 0 ? Math.max(...versions) : 1;
}
```

In `frontend/src/components/CommandPalette.tsx`, add the import with the other `lib` imports:

```ts
import { latestVersion } from "../lib/schema-version";
```

> Confirm the relative path from `src/components/` to `src/lib/` is `../lib/schema-version`; match the file's existing `lib` import style.

Replace line 225:

```ts
          latest: s.versions[s.versions.length - 1] ?? 1,
```

with:

```ts
          latest: latestVersion(s.versions),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && bun run test src/lib/schema-version.test.ts`
Expected: PASS.

- [ ] **Step 5: Run the frontend hard gate and commit**

```bash
git add frontend/src/lib/schema-version.ts frontend/src/lib/schema-version.test.ts frontend/src/components/CommandPalette.tsx
git commit -m "fix(frontend): compute latest schema version via Math.max"
```

---

### Task 10: Stop polling a deleted consumer group (Medium)

**Problem:** After deleting a group, the `group` search param is never cleared (`frontend/src/routes/clusters.$cluster.groups.index.tsx`), so `GroupDetailPanel` stays mounted for the deleted group and keeps polling every 5s, showing a persistent "Failed to load group" error. Fix: clear the selected group in the delete mutation's `onSuccess`.

**Files:**
- Modify: `frontend/src/routes/clusters.$cluster.groups.index.tsx` (delete mutation `onSuccess`)
- Verify: build + manual/e2e (route-level navigation)

**Interfaces:**
- Consumes: the delete `useMutation` (`delMut`) `onSuccess`, the page's group-selection setter. The page selects a group via the `group` search param and `setGroup` (passed to `GroupsTable` as `onSelect`).
- Produces: on successful delete, the selected group is cleared so `GroupDetailPanel` unmounts and stops polling.

- [ ] **Step 1: Locate the selection setter used for the search param**

Run: `grep -n "setGroup\|search:\s*{\s*group\|useNavigate\|navigate(" frontend/src/routes/clusters.\$cluster.groups.index.tsx`
Expected: the `group` value comes from the route search params and is set via a setter (e.g. `setGroup`) that updates `search.group`. Identify the exact setter name and how it clears (passing `undefined`).

- [ ] **Step 2: Clear the selection on delete success**

In the `delMut` mutation (the `useMutation` whose `mutationFn` is `() => deleteGroup(cluster, detail.group_id)`), extend `onSuccess` from:

```tsx
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups", cluster] });
    },
```

to also clear the selected group. If selection is driven by a `setGroup(value: string | undefined)` setter available in this component's scope, pass it in or call the navigate that clears the param:

```tsx
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups", cluster] });
      // Clear the selection so GroupDetailPanel unmounts and stops polling the
      // now-deleted group.
      onGroupDeleted?.();
    },
```

Then thread an `onGroupDeleted` callback from the page (where `setGroup`/the search-param setter lives) down to the component that owns `delMut`, wiring it to set the group param to `undefined`. If `delMut` lives in the same component that has the setter, call the setter directly instead of adding a prop:

```tsx
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups", cluster] });
      setGroup(undefined);
    },
```

> Pick whichever form matches the actual component boundary you found in Step 1. The non-negotiable outcome: after a successful delete, the `group` search param no longer references the deleted group.

- [ ] **Step 3: Type-check and run the unit suite**

Run: `cd frontend && bun run build && bun run test`
Expected: build PASSES; unit suite green.

- [ ] **Step 4: Manually/e2e verify**

Delete a consumer group while its detail panel is open; confirm the panel closes and no further `/groups/{group}` requests fire (DevTools network tab, or an e2e spec under `frontend/e2e/`). Record the verification in the PR body.

- [ ] **Step 5: Commit**

```bash
git add "frontend/src/routes/clusters.\$cluster.groups.index.tsx"
git commit -m "fix(frontend): clear selected group after delete to stop stale polling"
```

---

### Task 11: Refresh state from cross-tab storage changes (Medium)

**Problem:** Two related staleness bugs from the same root cause — components read `localStorage`-backed state without subscribing to changes: (a) the settings cluster table memoizes on a manual `tick` that `useCluster()` does not bump, so a private-cluster add/edit/delete from another tab does not refresh it (`frontend/src/routes/settings.clusters.tsx:113-118`); (b) `useCluster` reads `readStored()` in render with no `storage` listener (`frontend/src/lib/use-cluster.ts:124`). Fix: subscribe to the private-cluster change notifications and the `storage` event.

**Files:**
- Modify: `frontend/src/routes/settings.clusters.tsx` (subscribe to private-cluster changes)
- Modify: `frontend/src/lib/use-cluster.ts` (hold `stored` in state synced via a `storage` listener)
- Test: `frontend/src/lib/use-cluster.test.tsx` (create — `renderHook` + dispatch a `storage` event)

**Interfaces:**
- Consumes: the private-clusters subscription API (`subscribePrivateClusters` / equivalent — confirm the export in `lib/private-clusters.ts`), `window` `storage` event, `readStored()`.
- Produces: `useCluster` re-renders on a `storage` event; the settings table refreshes on any private-cluster mutation from another tab.

- [ ] **Step 1: Confirm the subscription primitive**

Run: `grep -n "subscribe\|addEventListener\|storage\|dispatchEvent\|export function" frontend/src/lib/private-clusters.ts`
Expected: identify the exported subscribe function (the audit referenced `subscribePrivateClusters`). If it does not exist, the storage-event approach in Step 3 covers both files; note that in the commit.

- [ ] **Step 2: Write the failing test for useCluster**

Create `frontend/src/lib/use-cluster.test.tsx`:

```tsx
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { useCluster } from "./use-cluster";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("useCluster", () => {
  it("re-renders when another tab writes the stored cluster (storage event)", () => {
    const { result } = renderHook(() => useCluster());
    const before = result.current.cluster;

    act(() => {
      localStorage.setItem("kafkito.cluster", "from-other-tab");
      window.dispatchEvent(new StorageEvent("storage", { key: "kafkito.cluster" }));
    });

    // The hook must have reacted to the storage event (value re-read).
    expect(result.current.cluster).not.toBe(undefined);
    expect(result.current.cluster).not.toEqual(before === "from-other-tab" ? null : before);
  });
});
```

> Read `use-cluster.ts` for the exact `localStorage` key (`readStored`/`writeStored` reference it — the audit cited `kafkito.cluster`). Use that key verbatim. If `useCluster` requires a Router context to run, wrap `renderHook` with the project's test router provider (check whether other hook tests in `src/lib/*.test.ts` provide one) or assert only that no throw occurs and the storage listener is attached. Keep the test minimal and order-independent.

- [ ] **Step 3: Add the storage listener to useCluster**

In `frontend/src/lib/use-cluster.ts`, replace the in-render read:

```ts
  const stored = readStored();
```

with a state-backed value synced on `storage`:

```ts
  const [stored, setStored] = useState<string | null>(() => readStored());
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      // Re-read on any kafkito storage change (or a generic clear with key null).
      if (e.key === null || e.key.startsWith("kafkito.")) {
        setStored(readStored());
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);
```

> Ensure `useState`/`useEffect` are imported from `react` in this file (add to the existing import if missing). The `cluster` `useMemo` already depends on `stored`, so it recomputes when `setStored` fires.

- [ ] **Step 4: Subscribe the settings table to private-cluster changes**

In `frontend/src/routes/settings.clusters.tsx`, replace the manual-tick memo (lines 113-118):

```tsx
  // Subscribe via the hook so list refreshes on any mutation.
  useCluster();
  const [tick, setTick] = useState(0);
  const items = useMemo(() => listPrivateClusters(), [tick]);
  const forceRefresh = () => setTick((n) => n + 1);
```

with a subscription-driven refresh (using the primitive confirmed in Step 1). If `subscribePrivateClusters(cb)` exists:

```tsx
  useCluster();
  const [items, setItems] = useState(() => listPrivateClusters());
  const forceRefresh = () => setItems(listPrivateClusters());
  useEffect(() => {
    const unsub = subscribePrivateClusters(() => setItems(listPrivateClusters()));
    const onStorage = (e: StorageEvent) => {
      if (e.key === null || e.key.startsWith("kafkito.")) setItems(listPrivateClusters());
    };
    window.addEventListener("storage", onStorage);
    return () => {
      unsub?.();
      window.removeEventListener("storage", onStorage);
    };
  }, []);
```

> Add the `subscribePrivateClusters` import (or, if it does not exist, keep only the `storage`-listener branch). Ensure `useEffect` is imported. `forceRefresh` keeps its existing call sites working.

- [ ] **Step 5: Run tests and the build**

Run: `cd frontend && bun run test src/lib/use-cluster.test.tsx && bun run build`
Expected: PASS and a clean type-check.

- [ ] **Step 6: Run the frontend hard gate and commit**

```bash
git add frontend/src/lib/use-cluster.ts frontend/src/lib/use-cluster.test.tsx frontend/src/routes/settings.clusters.tsx
git commit -m "fix(frontend): react to cross-tab storage and private-cluster changes"
```

---

### Final verification (after all tasks)

- [ ] **Run the full frontend hard gate**

Run: `cd frontend && bun run lint && bun run build && bun run check:palette && bun run check:strings && bun run check:tokens && bun run check:routes && bun run check:dates && bun run test`
Expected: every command exits 0.

- [ ] **Spot-check the touched UI in light + dark mode** against the running dev server (`bun run dev`): reset-offsets confirm dialog (stays open on a failed commit), PathSense (closes on outside click), command palette schema "latest", and the groups page (detail panel closes after delete).

- [ ] **Confirm no new dependencies were added**

Run: `git diff main -- frontend/package.json frontend/bun.lock`
Expected: empty diff.

---

## Notes for the implementer

- Tasks are independent; ship the High items first (Tasks 1, 2, 4, 5, 7).
- Tasks 1, 2, 3 all touch `auth/`; if done out of order, reconcile imports once.
- Where a step says "confirm the exact name/path/shape", that is a read-and-verify instruction, not a placeholder — the surrounding code in the named file is the source of truth; do not invent names.
- Do not batch commits across tasks — each ends green on its own behind the frontend hard gate.
</content>
