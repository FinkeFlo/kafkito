import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  fetchMessages,
  searchMessages,
  type Message,
  type PartitionInfo,
  type SearchMode,
  type SearchOp,
  type SearchDirection,
  type SearchStats,
  type SearchRequest,
} from "@/lib/api";
import {
  isTooLargeToScan,
  MAX_HYDRATE_VALUE_BYTES,
  type HydratableEncoding,
} from "@/lib/hydrate-sample";
import { buildPathTree } from "@/lib/path-tree";
import { buildXmlPathTree, looksLikeXml } from "@/lib/xml-path-tree";
import { buildJsonPath, wildcardArrayIndices, type Token } from "@/lib/path-builder";
import { PathSense } from "@/features/messages/PathSense";
import { MessageRangeCountPreview } from "@/features/messages/MessageRangeCountPreview";
import { useFormatters } from "@/lib/use-formatters";
import { BulkCopyPanel } from "@/features/messages/BulkCopyPanel";
import { MessageRow } from "@/features/messages/MessageRow";
import { RangePicker } from "@/features/messages/RangePicker";
import { StatusBox, StatusIcon } from "@/components/ui/StatusIcon";
import { messageQueries } from "@/lib/queries/messages";
import { topicQueries } from "@/lib/queries/topics";
import { computeTimeRange } from "@/features/messages/time-range";
import { orderForDisplay, type SortOrder } from "@/features/messages/display-order";
import {
  clampLimit,
  clampOffset,
  offsetBoundsFor,
  type BrowseFrom,
} from "@/features/messages/browse-params";
import { useNumberDraft } from "@/features/messages/use-number-draft";
import { useMessagesSearchParams } from "@/features/messages/use-messages-search-params";

interface MessagesSearch {
  partition: number;
  limit: number;
  from: BrowseFrom;
  msgOffset: number;
}

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/messages")({
  validateSearch: (s: Record<string, unknown>): MessagesSearch => {
    const fromRaw = s.from;
    const from: BrowseFrom = fromRaw === "start" || fromRaw === "offset" ? fromRaw : "end";
    return {
      partition: typeof s.partition === "number" ? s.partition : -1,
      limit: typeof s.limit === "number" ? s.limit : 50,
      from,
      msgOffset: typeof s.msgOffset === "number" ? s.msgOffset : 0,
    };
  },
  component: MessagesTab,
});

function MessagesTab() {
  const { cluster, topic } = Route.useParams();
  const [copyOpen, setCopyOpen] = useState(false);

  const detailQuery = useQuery({
    ...topicQueries.detail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: 5_000,
  });

  if (!detailQuery.data) {
    return <div className="text-sm text-[var(--color-text-muted)]">Loading…</div>;
  }

  const partitionNumbers = detailQuery.data.partitions.map((p) => p.partition);

  return (
    <div className="space-y-4">
      <MessagesPanel cluster={cluster} topic={topic} partitions={detailQuery.data.partitions} />

      {/* Bulk copy section */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-sm">
        <button
          type="button"
          onClick={() => setCopyOpen((v) => !v)}
          className="flex w-full items-center justify-between px-4 py-3 text-left text-sm font-semibold text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
        >
          <span>Copy messages to another cluster / topic</span>
          <span className="text-[var(--color-text-subtle)]">{copyOpen ? "▾" : "▸"}</span>
        </button>
        {copyOpen && (
          <div className="border-t border-[var(--color-border)] p-4">
            <BulkCopyPanel srcCluster={cluster} srcTopic={topic} partitions={partitionNumbers} />
          </div>
        )}
      </div>
    </div>
  );
}

function MessagesPanel({
  cluster,
  topic,
  partitions,
}: {
  cluster: string;
  topic: string;
  partitions: PartitionInfo[];
}) {
  const fmt = useFormatters();
  const { partition, limit, from, msgOffset, setPartition, setLimit, setFrom, setMsgOffset } =
    useMessagesSearchParams();

  const offsetBounds = useMemo(
    () => offsetBoundsFor(partitions, partition),
    [partition, partitions],
  );

  const {
    draft: offsetDraft,
    setDraft: setOffsetDraft,
    commit: commitOffset,
  } = useNumberDraft(msgOffset, (raw) => clampOffset(raw, offsetBounds), setMsgOffset);

  const [live, setLive] = useState<boolean>(false);

  const {
    draft: limitDraft,
    setDraft: setLimitDraft,
    commit: commitLimit,
  } = useNumberDraft(limit, clampLimit, setLimit);

  const [sortOrder, setSortOrder] = useState<SortOrder>("newest");

  // Browse-level time-range filter (separate state from the search panel below)
  const [browseRangeMode, setBrowseRangeMode] = useState<"off" | "preset" | "custom">("off");
  const [browsePreset, setBrowsePreset] = useState<string>("24h");
  const [browseCustomFrom, setBrowseCustomFrom] = useState<string>("");
  const [browseCustomTo, setBrowseCustomTo] = useState<string>("");

  // Search state
  const [searchOpen, setSearchOpen] = useState(false);
  const [mode, setMode] = useState<SearchMode>("contains");
  const [path, setPath] = useState("");
  const [op, setOp] = useState<SearchOp>("contains");
  const [needle, setNeedle] = useState("");
  const [rangeMode, setRangeMode] = useState<"off" | "preset" | "custom">("off");
  const [preset, setPreset] = useState<string>("24h");
  const [customFrom, setCustomFrom] = useState<string>("");
  const [customTo, setCustomTo] = useState<string>("");
  const [direction, setDirection] = useState<SearchDirection>("newest_first");
  const [stopOnLimit, setStopOnLimit] = useState(true);
  const [budget, setBudget] = useState(50000);
  const [budgetUnlimited, setBudgetUnlimited] = useState(false);
  const [searching, setSearching] = useState(false);
  const [searchResult, setSearchResult] = useState<{
    messages: Message[];
    stats: SearchStats;
    req: SearchRequest;
  } | null>(null);
  const [searchError, setSearchError] = useState<string | null>(null);
  // Why the most recent (auto-chained) search stopped. Drives the result banner.
  const [searchStopReason, setSearchStopReason] = useState<
    "budget" | "complete" | "limit" | "stopped" | null
  >(null);
  // Set to true to abort an in-flight auto-chain between continuation calls.
  const stopSearchRef = useRef(false);

  // Sample query (lazy, only when a structured — JSONPath or XPath — search
  // is open). Field-path suggestions need each sample message's full
  // structure, but the sample endpoint returns the same 64 KB-truncated
  // preview as the message list — silently starving PathSense of any field
  // that only appears past the truncation boundary (or dropping the message
  // outright, since truncated JSON/XML usually fails to parse).
  // hydrateTruncatedSampleMessages fetches the full raw value for any
  // truncated sample, falling back to the truncated preview on failure.
  //
  // Keyed by encoding, not by mode, so the two structured modes share a
  // cache entry whenever they hydrate the same thing. Hydration only fetches
  // values the active tree can parse: pulling up to MAX_HYDRATE_VALUE_BYTES
  // per sample for the builder that will discard them is pure waste.
  const sampleEncoding: HydratableEncoding = mode === "xpath" ? "xml" : "json";
  const sampleQuery = useQuery({
    ...messageQueries.sample(cluster, topic, sampleEncoding),
    enabled: searchOpen && (mode === "jsonpath" || mode === "xpath"),
  });

  const pathTree = useMemo(() => {
    const msgs = sampleQuery.data?.messages ?? [];
    const parsed: unknown[] = msgs
      .map((m) => {
        try {
          return JSON.parse(m.value ?? "");
        } catch {
          return null;
        }
      })
      // Arrays are kept: a record whose whole value is an array of rows is a
      // normal payload shape, and buildPathTree indexes it under `$[*]`.
      // Scalars carry no field paths and are dropped by the builder itself.
      .filter((v): v is object => v !== null && typeof v === "object");
    return buildPathTree(parsed);
  }, [sampleQuery.data]);

  // A sample above the hydration cap never gets its full value, so the tree
  // is built from a 64 KB fragment that is cut mid-structure and therefore
  // doesn't parse. Reporting that as "isn't JSON" is simply untrue — the
  // value is valid, it is just too large to scan for field names.
  const sampleTooLargeToScan = useMemo(
    () => (sampleQuery.data?.messages ?? []).some(isTooLargeToScan),
    [sampleQuery.data],
  );

  // XPath's suggestion tree is built from the same (already hydrated)
  // samples, parsed with the browser's DOMParser rather than JSON.parse.
  const xmlPathTree = useMemo(() => {
    const values = (sampleQuery.data?.messages ?? [])
      .map((m) => m.value ?? "")
      .filter(looksLikeXml);
    return buildXmlPathTree(values);
  }, [sampleQuery.data]);

  const [undoToast, setUndoToast] = useState<{
    previous: { path: string; op: SearchOp; needle: string };
    until: number;
  } | null>(null);

  const finalizePick = (trail: Token[], leafValue: unknown) => {
    const previous = { path, op, needle };
    const hadAnyInput = path.trim() !== "" || needle.trim() !== "";

    setMode("jsonpath");
    setPath(buildJsonPath(trail));
    if (leafValue !== undefined) {
      setOp("eq");
      setNeedle(String(leafValue));
    } else {
      setOp("exists");
      setNeedle("");
    }

    if (hadAnyInput) {
      setUndoToast({ previous, until: Date.now() + 4000 });
    }
  };

  useEffect(() => {
    if (!undoToast) return;
    const remaining = undoToast.until - Date.now();
    if (remaining <= 0) {
      setUndoToast(null);
      return;
    }
    const timer = setTimeout(() => setUndoToast(null), remaining);
    return () => clearTimeout(timer);
  }, [undoToast]);

  const handlePick = (trail: Token[], leafValue: unknown) => {
    setSearchOpen(true);
    finalizePick(wildcardArrayIndices(trail), leafValue);
  };

  const [showCoachmark, setShowCoachmark] = useState(() => {
    try {
      return localStorage.getItem("kafkito.coachmark.livejson.seen") !== "1";
    } catch {
      return false;
    }
  });

  const dismissCoachmark = useCallback(() => {
    setShowCoachmark(false);
    try {
      localStorage.setItem("kafkito.coachmark.livejson.seen", "1");
    } catch {
      // ignore quota / privacy-mode failures
    }
  }, []);

  const browseRange = useMemo(
    () => computeTimeRange(browseRangeMode, browsePreset, browseCustomFrom, browseCustomTo),
    [browseRangeMode, browsePreset, browseCustomFrom, browseCustomTo],
  );

  const params = useMemo(
    () => ({
      partition,
      limit,
      from,
      // Single partition selected: seek that partition. Partition = all:
      // seek every partition to the same offset via partition_offsets so
      // "from offset" works across the whole topic, not just one partition.
      offset: from === "offset" && partition >= 0 ? msgOffset : undefined,
      partitionOffsets:
        from === "offset" && partition < 0 && partitions.length > 0
          ? Object.fromEntries(partitions.map((p) => [p.partition, msgOffset]))
          : undefined,
      from_ts_ms: browseRange.from_ts_ms,
      to_ts_ms: browseRange.to_ts_ms,
    }),
    [partition, limit, from, msgOffset, partitions, browseRange.from_ts_ms, browseRange.to_ts_ms],
  );

  const msgsQuery = useQuery({
    ...messageQueries.page(cluster, topic, params),
    refetchInterval: live ? 2_000 : false,
    enabled: !searchResult,
  });

  // Cursor pagination: the head page is fetched by the useQuery above; each
  // "Load more" click appends the next backward page using the previous
  // page's next_cursor. Reset whenever the head-page params change so the
  // accumulated tail can never out-of-sync with the current filter.
  const [tailMessages, setTailMessages] = useState<Message[]>([]);
  const [tailCursor, setTailCursor] = useState<string | undefined>(undefined);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const loadGenRef = useRef(0);

  useEffect(() => {
    loadGenRef.current += 1;
    setTailMessages([]);
    setTailCursor(msgsQuery.data?.next_cursor);
    setLoadMoreError(null);
    setLoadingMore(false);
  }, [msgsQuery.data]);

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

  const resolvedRange = () => computeTimeRange(rangeMode, preset, customFrom, customTo);

  const runSearch = async (continueChain = false) => {
    stopSearchRef.current = false;
    setSearching(true);
    setSearchError(null);
    // A fresh search clears any previous result immediately so the list shows
    // search results (empty until the first match arrives) rather than the
    // browse messages while scanning is still in progress.
    if (!continueChain) {
      setSearchResult(null);
      setSearchStopReason(null);
    }
    const { from_ts_ms, to_ts_ms } = resolvedRange();
    const baseReq: SearchRequest = {
      partition,
      limit,
      direction,
      stop_on_limit: stopOnLimit,
      mode,
      path: mode === "contains" || mode === "js" ? "" : path,
      op: mode === "contains" || mode === "js" ? "contains" : op,
      value: needle,
      zones: mode === "contains" ? ["value", "key", "headers"] : ["value"],
      from_ts_ms,
      to_ts_ms,
    };

    // Seed the accumulator from the existing result when continuing ("Search
    // more"), otherwise start fresh. The Budget applies per invocation: a fresh
    // search scans up to `budget` records; "Search more" grants another budget.
    const prior = continueChain ? searchResult : null;
    let accMessages: Message[] = prior ? [...prior.messages] : [];
    let accScanned = prior ? prior.stats.scanned : 0;
    let accMatched = prior ? prior.stats.matched : 0;
    let cursors: Record<string, number> | undefined = prior ? prior.stats.next_cursors : undefined;

    const budgetTarget = budgetUnlimited ? 0 : budget;
    // Budget 0 / empty means "scan the entire topic": keep chaining until the
    // range is exhausted (more_available=false), the limit is hit, or Stop.
    const unlimited = budgetTarget <= 0;
    // Per-call cap used in unlimited mode; the 12s server timeout is the real
    // limiter, this just keeps each request bounded.
    const PER_CALL_BUDGET = 1_000_000;
    let scannedThisRun = 0;
    let reason: "budget" | "complete" | "limit" | "stopped" = "complete";

    try {
      for (;;) {
        let callBudget: number;
        if (unlimited) {
          callBudget = PER_CALL_BUDGET;
        } else {
          const remaining = budgetTarget - scannedThisRun;
          if (remaining <= 0) {
            reason = "budget";
            break;
          }
          callBudget = remaining;
        }
        const req: SearchRequest = { ...baseReq, budget: callBudget, cursors };
        const r = await searchMessages(cluster, topic, req);
        const s = r.search;
        accMessages = [...accMessages, ...(r.messages ?? [])];
        accScanned += s.scanned;
        accMatched += s.matched;
        scannedThisRun += s.scanned;
        cursors = s.next_cursors;

        // Publish cumulative progress so the banner updates between calls.
        setSearchResult({
          messages: accMessages,
          stats: { ...s, scanned: accScanned, matched: accMatched },
          req,
        });

        if (!s.more_available) {
          reason = "complete";
          break;
        }
        if (stopOnLimit && accMatched >= limit) {
          reason = "limit";
          break;
        }
        if (stopSearchRef.current) {
          reason = "stopped";
          break;
        }
        // Safety: a call that scanned nothing but reports more would loop forever.
        if (s.scanned === 0) {
          reason = "complete";
          break;
        }
      }
      setSearchStopReason(reason);
    } catch (err) {
      setSearchError((err as Error).message);
    } finally {
      setSearching(false);
      stopSearchRef.current = false;
    }
  };

  const stopSearch = () => {
    stopSearchRef.current = true;
  };

  const clearSearch = () => {
    setSearchResult(null);
    setSearchError(null);
    setSearchStopReason(null);
  };

  const inSearchMode = searchResult !== null || searching;
  const rawMessages = inSearchMode
    ? (searchResult?.messages ?? [])
    : [...(msgsQuery.data?.messages ?? []), ...tailMessages];
  const displayMessages = useMemo(
    () => orderForDisplay(rawMessages, sortOrder),
    [rawMessages, sortOrder],
  );

  // Points the coachmark at a row that actually renders the click-to-filter
  // tree. Since the search fix the backend keeps reporting "json" for values
  // it truncated mid-structure, and those rows show a "Load full value"
  // button instead of a clickable tree — teaching on one would be misleading.
  const firstJsonIdx = displayMessages.findIndex(
    (m) => m.value_encoding === "json" && !m.value_truncated,
  );

  useEffect(() => {
    if (!showCoachmark) return;
    if (firstJsonIdx < 0) return; // don't burn the timer if there's no JSON to teach about
    const timer = setTimeout(dismissCoachmark, 8000);
    const onScroll = () => dismissCoachmark();
    window.addEventListener("scroll", onScroll, { once: true });
    return () => {
      clearTimeout(timer);
      window.removeEventListener("scroll", onScroll);
    };
  }, [showCoachmark, firstJsonIdx, dismissCoachmark]);

  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-sm">
      <div className="flex flex-wrap items-center gap-3 border-b border-[var(--color-border)] p-3">
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label htmlFor="browse-partition">Partition</label>
          <select
            id="browse-partition"
            value={partition}
            onChange={(e) => setPartition(Number(e.target.value))}
            className="rounded border border-[var(--color-border)] px-2 py-1 text-xs"
          >
            <option value={-1}>all</option>
            {partitions.map((p) => (
              <option key={p.partition} value={p.partition}>
                {p.partition}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label htmlFor="browse-from">From</label>
          <select
            id="browse-from"
            value={from}
            onChange={(e) => setFrom(e.target.value as BrowseFrom)}
            className="rounded border border-[var(--color-border)] px-2 py-1 text-xs"
            disabled={!!searchResult}
          >
            <option value="end">latest</option>
            <option value="start">oldest</option>
            <option value="offset">offset</option>
          </select>
          {from === "offset" && (
            <>
              <input
                aria-label="Start offset"
                value={offsetDraft}
                onChange={(e) => setOffsetDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    commitOffset();
                  }
                }}
                onBlur={commitOffset}
                inputMode="numeric"
                className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-xs font-mono"
                placeholder="0"
                disabled={!!searchResult}
                title={
                  offsetBounds
                    ? partition < 0
                      ? `Seeks every partition to this offset (valid ${fmt.number(offsetBounds.min)}–${fmt.number(offsetBounds.max)}). Press Enter to apply.`
                      : `Valid ${fmt.number(offsetBounds.min)}–${fmt.number(offsetBounds.max)}. Press Enter to apply.`
                    : "Press Enter to apply."
                }
              />
              {offsetBounds && (
                <span className="text-[var(--color-text-subtle)]">
                  {partition < 0 ? "all · " : ""}
                  {fmt.number(offsetBounds.min)}–{fmt.number(offsetBounds.max)}
                </span>
              )}
            </>
          )}
        </div>
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label htmlFor="browse-limit">Limit</label>
          <input
            id="browse-limit"
            type="number"
            min={1}
            max={500}
            value={limitDraft}
            onChange={(e) => setLimitDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                commitLimit();
              }
            }}
            onBlur={commitLimit}
            className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-xs"
          />
        </div>
        <fieldset
          aria-labelledby="browse-range"
          className="flex min-w-0 items-center gap-1.5 text-xs text-[var(--color-text-muted)]"
        >
          <span id="browse-range">Range</span>
          <RangePicker
            mode={browseRangeMode}
            preset={browsePreset}
            customFrom={browseCustomFrom}
            customTo={browseCustomTo}
            onChange={(m, p, f, t) => {
              setBrowseRangeMode(m);
              setBrowsePreset(p);
              setBrowseCustomFrom(f);
              setBrowseCustomTo(t);
            }}
            disabled={!!searchResult}
          />
        </fieldset>
        <MessageRangeCountPreview
          cluster={cluster}
          topic={topic}
          partition={partition}
          from_ts_ms={browseRange.from_ts_ms}
          to_ts_ms={browseRange.to_ts_ms}
          live={live}
        />
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label htmlFor="browse-sort">Sort</label>
          <select
            id="browse-sort"
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value as SortOrder)}
            className="rounded border border-[var(--color-border)] px-2 py-1 text-xs"
            title="Order of displayed messages"
          >
            <option value="newest">newest first</option>
            <option value="oldest">oldest first</option>
          </select>
        </div>
        <label className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <input
            type="checkbox"
            checked={live}
            onChange={(e) => setLive(e.target.checked)}
            className="h-3.5 w-3.5"
            disabled={!!searchResult}
          />
          Live
        </label>
        <button
          type="button"
          onClick={() => setSearchOpen((v) => !v)}
          className={`rounded border px-2 py-1 text-xs ${
            searchOpen
              ? "border-accent bg-accent-subtle text-accent"
              : "border-[var(--color-border)] hover:border-[var(--color-border-strong)]"
          }`}
        >
          {searchOpen ? "Close search" : "Search"}
        </button>
        <button
          type="button"
          onClick={() => msgsQuery.refetch()}
          className="ml-auto rounded border border-[var(--color-border)] px-2 py-1 text-xs hover:border-[var(--color-border-strong)]"
          disabled={!!searchResult}
        >
          Refresh
        </button>
        <span data-testid="messages-count" className="text-xs text-[var(--color-text-muted)]">
          {inSearchMode
            ? fmt.number(searchResult?.stats.matched ?? 0)
            : fmt.number(displayMessages.length)}
          {!inSearchMode && msgsQuery.isFetching && " · fetching…"}
          {searching && ` · ${fmt.number(searchResult?.stats.scanned ?? 0)} scanned · searching…`}
        </span>
      </div>

      {searchOpen && (
        <div className="space-y-3 border-b border-[var(--color-border)] bg-[var(--color-surface-subtle)] p-3">
          {undoToast && (
            <div className="flex items-center gap-3 rounded border border-border bg-panel p-2 text-xs">
              <span>Path replaced by click.</span>
              <button
                type="button"
                onClick={() => {
                  setPath(undoToast.previous.path);
                  setOp(undoToast.previous.op);
                  setNeedle(undoToast.previous.needle);
                  setUndoToast(null);
                }}
                className="rounded border border-border px-2 py-0.5 hover:border-border-strong"
              >
                Undo
              </button>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <label className="font-medium" htmlFor="search-mode">
              Mode
            </label>
            <select
              id="search-mode"
              value={mode}
              onChange={(e) => setMode(e.target.value as SearchMode)}
              className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
            >
              <option value="contains">Text contains</option>
              <option value="jsonpath">JSONPath</option>
              <option value="xpath">XPath</option>
              <option value="js">JavaScript</option>
            </select>

            {mode !== "contains" && mode !== "js" && (
              <>
                <label className="font-medium" htmlFor="search-path">
                  Path
                </label>
                {/* Both structured modes get PathSense; only the suggestion
                    tree and its XML/JSON-flavored copy differ. */}
                <div className="w-56">
                  <PathSense
                    id="search-path"
                    tree={mode === "xpath" ? xmlPathTree : pathTree}
                    value={path}
                    onChange={setPath}
                    placeholder={mode === "xpath" ? "//order/@status" : "Type or ↓ for top fields"}
                    emptyMessage={
                      sampleTooLargeToScan
                        ? `Sample is larger than ${fmt.bytes(MAX_HYDRATE_VALUE_BYTES)} — too large to scan for field names. Enter path manually.`
                        : mode === "xpath"
                          ? "Sample isn't XML or topic is empty — enter path manually."
                          : "Sample isn't JSON or topic is empty — enter path manually."
                    }
                    arrayIndexToggle={mode !== "xpath"}
                    onPick={(picked, type) => {
                      setPath(picked);
                      // The tree carries field names only, so the value is
                      // yours to type. A container can't be compared with
                      // `eq` at all, so it gets `exists` semantics.
                      const isScalar = type !== "object" && type !== "array";
                      setOp(isScalar ? "eq" : "exists");
                      setNeedle("");
                    }}
                  />
                </div>
                <label className="font-medium" htmlFor="search-operator">
                  Operator
                </label>
                <select
                  id="search-operator"
                  value={op}
                  onChange={(e) => setOp(e.target.value as SearchOp)}
                  className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
                >
                  <option value="exists">exists</option>
                  <option value="eq">=</option>
                  <option value="ne">≠</option>
                  <option value="contains">contains</option>
                  <option value="regex">regex</option>
                  <option value="gt">&gt;</option>
                  <option value="gte">≥</option>
                  <option value="lt">&lt;</option>
                  <option value="lte">≤</option>
                </select>
              </>
            )}
            <label
              className="font-medium"
              htmlFor={mode === "js" ? "search-expression" : "search-value"}
            >
              {mode === "js" ? "Expression" : "Value"}
            </label>
            {mode === "js" ? (
              <textarea
                id="search-expression"
                value={needle}
                onChange={(e) => setNeedle(e.target.value)}
                placeholder={'parsed && parsed.amount > 1000 && key.startsWith("ord-")'}
                className="min-h-[2.2rem] w-full flex-1 rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 font-mono"
                rows={2}
              />
            ) : (
              <input
                id="search-value"
                value={needle}
                onChange={(e) => setNeedle(e.target.value)}
                placeholder={
                  mode === "contains"
                    ? "Substring"
                    : op === "exists"
                      ? "(ignored)"
                      : "e.g. 42 / shipped / ^A.*"
                }
                className="w-56 rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 font-mono"
                disabled={mode !== "contains" && op === "exists"}
              />
            )}
          </div>
          {mode === "js" && (
            <div className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-2 text-[11px] text-[var(--color-text-muted)]">
              Variables: <code className="font-mono">key</code>,{" "}
              <code className="font-mono">value</code> (string),{" "}
              <code className="font-mono">parsed</code> (JSON),{" "}
              <code className="font-mono">headers</code>,{" "}
              <code className="font-mono">partition</code>,{" "}
              <code className="font-mono">offset</code>,{" "}
              <code className="font-mono">timestampMs</code>. Example:{" "}
              <code className="font-mono">
                parsed &amp;&amp; parsed.currency === "EUR" &amp;&amp; parsed.amount &gt; 500
              </code>
              . Limit 100 ms per message.
            </div>
          )}

          <div className="flex flex-wrap items-center gap-2 text-xs">
            <label className="font-medium" htmlFor="search-range">
              Range
            </label>
            <select
              id="search-range"
              value={rangeMode}
              onChange={(e) => setRangeMode(e.target.value as typeof rangeMode)}
              className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
            >
              <option value="off">off</option>
              <option value="preset">Preset</option>
              <option value="custom">Custom</option>
            </select>
            {rangeMode === "preset" && (
              <div className="flex flex-wrap items-center gap-1">
                {(["5m", "15m", "1h", "6h", "24h", "7d", "30d", "today", "yesterday"] as const).map(
                  (p) => (
                    <button
                      type="button"
                      key={p}
                      onClick={() => setPreset(p)}
                      className={`rounded border px-2 py-1 ${
                        preset === p
                          ? "border-accent bg-accent-subtle text-accent"
                          : "border-[var(--color-border)] bg-[var(--color-surface-raised)] hover:border-[var(--color-border-strong)]"
                      }`}
                    >
                      {p === "today" ? "Today" : p === "yesterday" ? "Yesterday" : `Last ${p}`}
                    </button>
                  ),
                )}
              </div>
            )}
            {rangeMode === "custom" && (
              <>
                <input
                  type="datetime-local"
                  aria-label="Search range from"
                  value={customFrom}
                  onChange={(e) => setCustomFrom(e.target.value)}
                  className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
                />
                <span>→</span>
                <input
                  type="datetime-local"
                  aria-label="Search range to"
                  value={customTo}
                  onChange={(e) => setCustomTo(e.target.value)}
                  className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
                />
              </>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-3 text-xs">
            <div className="flex items-center gap-1.5">
              <label className="font-medium" htmlFor="search-direction">
                Direction
              </label>
              <select
                id="search-direction"
                value={direction}
                onChange={(e) => setDirection(e.target.value as SearchDirection)}
                className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
              >
                <option value="newest_first">new → old</option>
                <option value="oldest_first">old → new</option>
              </select>
            </div>
            <label className="flex items-center gap-1.5">
              <input
                type="checkbox"
                checked={stopOnLimit}
                onChange={(e) => setStopOnLimit(e.target.checked)}
                className="h-3.5 w-3.5"
              />
              Stop after Limit matches ({fmt.number(limit)})
            </label>
            <label className="flex items-center gap-1.5">
              <input
                type="checkbox"
                checked={budgetUnlimited}
                onChange={(e) => setBudgetUnlimited(e.target.checked)}
                className="h-3.5 w-3.5"
              />
              Scan whole topic
            </label>
            <div className="flex items-center gap-1.5">
              <label
                htmlFor="search-budget"
                className={`font-medium ${
                  budgetUnlimited ? "text-[var(--color-text-subtle)]" : ""
                }`}
              >
                Max messages to scan
              </label>
              <input
                id="search-budget"
                type="number"
                min={1}
                max={1000000}
                step={1000}
                value={budget === 0 ? "" : budget}
                disabled={budgetUnlimited}
                placeholder="50000"
                title="Max number of messages to scan per search. Enable 'Scan whole topic' to scan everything."
                onChange={(e) => setBudget(e.target.value === "" ? 0 : Number(e.target.value) || 0)}
                className="w-24 rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 disabled:opacity-50"
              />
            </div>
            <button
              type="button"
              onClick={() => runSearch()}
              disabled={
                searching ||
                (mode === "js" && needle.trim() === "") ||
                (mode !== "contains" && mode !== "js" && path.trim() === "")
              }
              className="rounded bg-accent px-3 py-1 font-semibold text-[var(--color-text-on-accent)] hover:bg-accent-hover disabled:opacity-50"
            >
              {searching ? "Searching…" : "Search"}
            </button>
            {searching && (
              <button
                type="button"
                onClick={stopSearch}
                className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 hover:border-[var(--color-border-strong)]"
              >
                Stop
              </button>
            )}
            {searchResult && !searching && (
              <button
                type="button"
                onClick={clearSearch}
                className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 hover:border-[var(--color-border-strong)]"
              >
                Clear result
              </button>
            )}
          </div>

          {searchError && <StatusBox intent="danger">{searchError}</StatusBox>}
          {searchResult && (
            <div className="flex flex-wrap items-center gap-3 rounded border border-accent/30 bg-accent-subtle p-2 text-xs">
              <span className="font-semibold text-accent">
                {fmt.number(searchResult.stats.matched)} matches
              </span>
              <span className="text-[var(--color-text-muted)]">
                · {fmt.number(searchResult.stats.scanned)} scanned
                {searching && " …"}
              </span>
              {searchResult.stats.parse_errors > 0 && (
                <span
                  className="text-[var(--color-warning)] cursor-default"
                  title={
                    searchResult.stats.parse_error_offsets &&
                    searchResult.stats.parse_error_offsets.length > 0
                      ? searchResult.stats.parse_error_offsets
                          .map((e) => `p${e.partition}@${e.offset}: ${e.error}`)
                          .join("\n")
                      : undefined
                  }
                >
                  · {fmt.number(searchResult.stats.parse_errors)} parse errors skipped
                </span>
              )}
              {!searching && searchStopReason === "budget" && (
                <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
                  Scan limit reached
                </span>
              )}
              {!searching && searchStopReason === "limit" && (
                <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
                  Limit reached
                </span>
              )}
              {!searching && searchStopReason === "stopped" && (
                <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
                  Stopped
                </span>
              )}
              {!searching &&
                searchStopReason === "complete" &&
                !searchResult.stats.more_available && (
                  <span className="rounded bg-[var(--color-success-subtle)] px-1.5 py-0.5 text-[var(--color-success)]">
                    Range fully scanned
                  </span>
                )}
              {!searching && (
                <span className="ml-auto flex items-center gap-3">
                  {searchResult.stats.more_available && (
                    <button
                      type="button"
                      onClick={() => runSearch(true)}
                      className="rounded border border-[var(--color-border)] px-2 py-1 hover:border-[var(--color-border-strong)]"
                    >
                      Search more →
                    </button>
                  )}
                </span>
              )}
            </div>
          )}
        </div>
      )}

      {msgsQuery.error && !searchResult && (
        <StatusBox intent="danger" className="m-3 p-3 text-sm">
          {(msgsQuery.error as Error).message}
        </StatusBox>
      )}

      {!msgsQuery.error && !searchResult && msgsQuery.data?.partial && (
        <StatusBox intent="warning" className="m-3 p-3 text-sm">
          This page may be incomplete — a very large record delayed loading past the server's
          timeout, so the newest message(s) might be missing. Try Refresh.
        </StatusBox>
      )}

      {displayMessages.length === 0 && searching && (
        <div className="p-8 text-center text-sm text-[var(--color-text-subtle)]">
          Searching… {fmt.number(searchResult?.stats.scanned ?? 0)} scanned, no match yet.
        </div>
      )}

      {displayMessages.length === 0 && !searching && msgsQuery.isLoading && (
        <div className="p-8 text-center text-sm text-[var(--color-text-subtle)]">
          Loading messages…
        </div>
      )}

      {/* `isLoading` (not `isPending`) is the right guard: the query is
          disabled while a search result is on screen, and a disabled query
          stays `pending` forever — which would hide the "No matches." state. */}
      {displayMessages.length === 0 && !searching && !msgsQuery.isLoading && (
        <div className="p-8 text-center text-sm text-[var(--color-text-subtle)]">
          {searchResult ? "No matches." : "No messages."}
        </div>
      )}

      {showCoachmark && firstJsonIdx >= 0 && (
        <div className="m-3 flex items-center gap-2 rounded border border-accent/40 bg-accent-subtle p-2 text-xs text-accent">
          <span>Tip: click any value in a JSON message to filter by it.</span>
          <button
            type="button"
            onClick={dismissCoachmark}
            className="ml-auto rounded border border-border px-2 py-0.5 hover:border-border-strong"
          >
            Got it
          </button>
        </div>
      )}

      <div className="divide-y divide-[var(--color-border)]">
        {displayMessages.map((m) => (
          <MessageRow key={`${m.partition}-${m.offset}`} m={m} onPick={handlePick} />
        ))}
      </div>

      {!inSearchMode && tailCursor && !live && (
        <div className="flex flex-col items-center gap-2 p-4">
          <button
            type="button"
            onClick={loadMore}
            disabled={loadingMore}
            className="rounded-md border border-[var(--color-border)] bg-[var(--color-panel)] px-4 py-2 text-sm font-medium text-[var(--color-text)] transition-colors hover:border-[var(--color-border-hover)] disabled:cursor-not-allowed disabled:opacity-50"
          >
            {loadingMore ? "Loading…" : "Load more"}
          </button>
          {loadMoreError && (
            <div className="flex items-center gap-1 text-xs text-[var(--color-danger)]">
              <StatusIcon intent="danger" />
              {loadMoreError}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
