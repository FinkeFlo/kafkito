import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import {
  fetchTopicDetail,
  fetchMessages,
  fetchSample,
  searchMessages,
  type Message,
  type PartitionInfo,
  type SampleResponse,
  type SearchMode,
  type SearchOp,
  type SearchDirection,
  type SearchStats,
  type SearchRequest,
} from "@/lib/api";
import { buildPathTree } from "@/lib/path-tree";
import { buildJsonPath, type Token } from "@/lib/path-builder";
import { PathSense } from "@/components/path-sense";
import { ArrayScopePopover } from "@/components/array-scope-popover";
import { JsonInteractive } from "@/components/json-interactive";

interface MessagesSearch {
  partition: number;
  limit: number;
  from: "end" | "start" | "offset";
  msgOffset: number;
}

const PRESET_DURATIONS_MS: Record<string, number> = {
  "5m": 5 * 60_000,
  "15m": 15 * 60_000,
  "1h": 60 * 60_000,
  "6h": 6 * 60 * 60_000,
  "24h": 24 * 60 * 60_000,
  "7d": 7 * 24 * 60 * 60_000,
  "30d": 30 * 24 * 60 * 60_000,
};

function computeTimeRange(
  mode: "off" | "preset" | "custom",
  preset: string,
  customFrom: string,
  customTo: string,
): { from_ts_ms: number | undefined; to_ts_ms: number | undefined } {
  if (mode === "off") return { from_ts_ms: undefined, to_ts_ms: undefined };
  if (mode === "preset") {
    const now = Date.now();
    if (preset === "today") {
      const s = new Date();
      s.setHours(0, 0, 0, 0);
      return { from_ts_ms: s.getTime(), to_ts_ms: now };
    }
    if (preset === "yesterday") {
      const s = new Date();
      s.setHours(0, 0, 0, 0);
      s.setDate(s.getDate() - 1);
      const e = new Date(s);
      e.setHours(23, 59, 59, 999);
      return { from_ts_ms: s.getTime(), to_ts_ms: e.getTime() };
    }
    return {
      from_ts_ms: now - (PRESET_DURATIONS_MS[preset] ?? 24 * 60 * 60_000),
      to_ts_ms: now,
    };
  }
  return {
    from_ts_ms: customFrom ? new Date(customFrom).getTime() : undefined,
    to_ts_ms: customTo ? new Date(customTo).getTime() : undefined,
  };
}

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/messages")({
  validateSearch: (s: Record<string, unknown>): MessagesSearch => {
    const fromRaw = s.from;
    const from: "end" | "start" | "offset" =
      fromRaw === "start" || fromRaw === "offset" ? fromRaw : "end";
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

  const detailQuery = useQuery({
    queryKey: ["topic", cluster, topic],
    queryFn: () => fetchTopicDetail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: 5_000,
  });

  if (!detailQuery.data) {
    return <div className="text-sm text-[var(--color-text-muted)]">Loading…</div>;
  }

  return (
    <MessagesPanel
      cluster={cluster}
      topic={topic}
      partitions={detailQuery.data.partitions}
    />
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
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const { partition, limit, from, msgOffset } = search;

  const setPartition = (v: number) =>
    navigate({ search: (prev) => ({ ...prev, partition: v }) });
  const setLimit = (v: number) =>
    navigate({ search: (prev) => ({ ...prev, limit: v }) });
  const setFrom = (v: "end" | "start" | "offset") =>
    navigate({ search: (prev) => ({ ...prev, from: v }) });
  const setMsgOffset = (v: number) =>
    navigate({ search: (prev) => ({ ...prev, msgOffset: v }) });

  const [live, setLive] = useState<boolean>(false);
  const [sortOrder, setSortOrder] = useState<"newest" | "oldest">("newest");

  // Browse-level time-range filter (separate state from the search panel below)
  const [browseRangeMode, setBrowseRangeMode] = useState<
    "off" | "preset" | "custom"
  >("off");
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
  const [budget, setBudget] = useState(10000);
  const [searching, setSearching] = useState(false);
  const [searchResult, setSearchResult] = useState<
    | { messages: Message[]; stats: SearchStats; req: SearchRequest }
    | null
  >(null);
  const [searchError, setSearchError] = useState<string | null>(null);

  // Sample query (lazy, only when JSONPath search is open)
  const sampleQuery = useQuery<SampleResponse>({
    queryKey: ["sample", cluster, topic],
    queryFn: () => fetchSample(cluster, topic, 5, -1),
    enabled: searchOpen && mode === "jsonpath",
    staleTime: 5 * 60_000,
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
      .filter(
        (v): v is Record<string, unknown> =>
          v !== null && typeof v === "object" && !Array.isArray(v),
      );
    return buildPathTree(parsed);
  }, [sampleQuery.data]);

  const [arrayPicker, setArrayPicker] = useState<
    | {
        trail: Token[];
        leafValue: unknown;
        arrayLengths: number[];
        arrayDepth: number;
      }
    | null
  >(null);

  const [undoToast, setUndoToast] = useState<
    | { previous: { path: string; op: SearchOp; needle: string }; until: number }
    | null
  >(null);

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

  const handlePick = (
    trail: Token[],
    leafValue: unknown,
    arrayLengths: number[],
  ) => {
    setSearchOpen(true);
    const lastIndexFromEnd = [...trail]
      .reverse()
      .findIndex((t) => t.kind === "index");
    if (lastIndexFromEnd === -1) {
      finalizePick(trail, leafValue);
      return;
    }
    const arrayDepth = trail.length - 1 - lastIndexFromEnd;
    setArrayPicker({ trail, leafValue, arrayLengths, arrayDepth });
  };

  const [showCoachmark, setShowCoachmark] = useState(() => {
    try {
      return localStorage.getItem("kafkito.coachmark.livejson.seen") !== "1";
    } catch {
      return false;
    }
  });

  const dismissCoachmark = () => {
    setShowCoachmark(false);
    try {
      localStorage.setItem("kafkito.coachmark.livejson.seen", "1");
    } catch {
      // ignore quota / privacy-mode failures
    }
  };

  const browseRange = useMemo(
    () =>
      computeTimeRange(
        browseRangeMode,
        browsePreset,
        browseCustomFrom,
        browseCustomTo,
      ),
    [browseRangeMode, browsePreset, browseCustomFrom, browseCustomTo],
  );

  const params = useMemo(
    () => ({
      partition,
      limit,
      from,
      offset: from === "offset" ? msgOffset : undefined,
      from_ts_ms: browseRange.from_ts_ms,
      to_ts_ms: browseRange.to_ts_ms,
    }),
    [partition, limit, from, msgOffset, browseRange.from_ts_ms, browseRange.to_ts_ms],
  );

  const msgsQuery = useQuery({
    queryKey: ["messages", cluster, topic, params],
    queryFn: () => fetchMessages(cluster, topic, params),
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

  useEffect(() => {
    setTailMessages([]);
    setTailCursor(msgsQuery.data?.next_cursor);
    setLoadMoreError(null);
  }, [msgsQuery.data]);

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

  const resolvedRange = () =>
    computeTimeRange(rangeMode, preset, customFrom, customTo);

  const runSearch = async (continuation?: Record<string, number>) => {
    setSearching(true);
    setSearchError(null);
    const { from_ts_ms, to_ts_ms } = resolvedRange();
    const req: SearchRequest = {
      partition,
      limit,
      budget,
      direction,
      stop_on_limit: stopOnLimit,
      mode,
      path: mode === "contains" || mode === "js" ? "" : path,
      op: mode === "contains" || mode === "js" ? "contains" : op,
      value: needle,
      zones: mode === "contains" ? ["value", "key", "headers"] : ["value"],
      from_ts_ms,
      to_ts_ms,
      cursors: continuation,
    };
    try {
      const r = await searchMessages(cluster, topic, req);
      setSearchResult((prev) => {
        const prior = continuation && prev ? prev.messages : [];
        return {
          messages: [...prior, ...(r.messages ?? [])],
          stats: r.search,
          req,
        };
      });
    } catch (err) {
      setSearchError((err as Error).message);
    } finally {
      setSearching(false);
    }
  };

  const clearSearch = () => {
    setSearchResult(null);
    setSearchError(null);
  };

  const rawMessages = searchResult
    ? searchResult.messages
    : [...(msgsQuery.data?.messages ?? []), ...tailMessages];
  const displayMessages = useMemo(() => {
    if (sortOrder === "oldest") return rawMessages;
    // Stable sort: newest timestamp first; ties broken by (partition, offset) desc
    // so concurrent records keep a deterministic order.
    return [...rawMessages].sort((a, b) => {
      if (b.timestamp_ms !== a.timestamp_ms) return b.timestamp_ms - a.timestamp_ms;
      if (b.partition !== a.partition) return b.partition - a.partition;
      return b.offset - a.offset;
    });
  }, [rawMessages, sortOrder]);

  const firstJsonIdx = displayMessages.findIndex(
    (m) => m.value_encoding === "json",
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
  }, [showCoachmark, firstJsonIdx]);

  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-sm">
      <div className="flex flex-wrap items-center gap-3 border-b border-[var(--color-border)] p-3">
        <div className="text-sm font-semibold">Messages</div>
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label>Partition</label>
          <select
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
          <label>From</label>
          <select
            value={from}
            onChange={(e) => setFrom(e.target.value as "end" | "start" | "offset")}
            className="rounded border border-[var(--color-border)] px-2 py-1 text-xs"
            disabled={!!searchResult}
          >
            <option value="end">latest</option>
            <option value="start">oldest</option>
            <option value="offset">offset</option>
          </select>
          {from === "offset" && (
            <input
              value={String(msgOffset)}
              onChange={(e) => setMsgOffset(Number(e.target.value) || 0)}
              className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-xs font-mono"
              placeholder="0"
              disabled={!!searchResult}
            />
          )}
        </div>
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label>Limit</label>
          <input
            type="number"
            min={1}
            max={500}
            value={limit}
            onChange={(e) => setLimit(Number(e.target.value) || 50)}
            className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-xs"
          />
        </div>
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label>Range</label>
          <select
            value={browseRangeMode}
            onChange={(e) =>
              setBrowseRangeMode(
                e.target.value as "off" | "preset" | "custom",
              )
            }
            className="rounded border border-[var(--color-border)] px-2 py-1 text-xs"
            disabled={!!searchResult}
            title="Filter messages by timestamp"
          >
            <option value="off">any time</option>
            <option value="preset">preset</option>
            <option value="custom">custom</option>
          </select>
        </div>
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
          <label>Sort</label>
          <select
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value as "newest" | "oldest")}
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
          onClick={() => msgsQuery.refetch()}
          className="ml-auto rounded border border-[var(--color-border)] px-2 py-1 text-xs hover:border-[var(--color-border-strong)]"
          disabled={!!searchResult}
        >
          Refresh
        </button>
        <span className="text-xs text-[var(--color-text-muted)]">
          {displayMessages.length}
          {msgsQuery.isFetching && !searchResult && " · fetching…"}
          {searching && " · searching…"}
        </span>
      </div>

      {browseRangeMode !== "off" && !searchResult && (
        <div className="flex flex-wrap items-center gap-2 border-b border-[var(--color-border)] bg-[var(--color-surface-subtle)] px-3 py-2 text-xs">
          {browseRangeMode === "preset" && (
            <>
              <span className="text-[var(--color-text-muted)]">From</span>
              {(
                ["5m", "15m", "1h", "6h", "24h", "7d", "30d", "today", "yesterday"] as const
              ).map((p) => (
                <button
                  key={p}
                  onClick={() => setBrowsePreset(p)}
                  className={`rounded border px-2 py-0.5 ${
                    browsePreset === p
                      ? "border-accent bg-accent-subtle text-accent"
                      : "border-[var(--color-border)] hover:border-[var(--color-border-strong)]"
                  }`}
                >
                  {p}
                </button>
              ))}
            </>
          )}
          {browseRangeMode === "custom" && (
            <>
              <label className="flex items-center gap-1.5 text-[var(--color-text-muted)]">
                From
                <input
                  type="datetime-local"
                  value={browseCustomFrom}
                  onChange={(e) => setBrowseCustomFrom(e.target.value)}
                  className="rounded border border-[var(--color-border)] px-2 py-1"
                />
              </label>
              <label className="flex items-center gap-1.5 text-[var(--color-text-muted)]">
                To
                <input
                  type="datetime-local"
                  value={browseCustomTo}
                  onChange={(e) => setBrowseCustomTo(e.target.value)}
                  className="rounded border border-[var(--color-border)] px-2 py-1"
                />
              </label>
            </>
          )}
        </div>
      )}

      {searchOpen && (
        <div className="space-y-3 border-b border-[var(--color-border)] bg-[var(--color-surface-subtle)] p-3">
          {arrayPicker && (
            <div className="mb-3">
              <ArrayScopePopover
                arrayPath={buildJsonPath(
                  arrayPicker.trail.slice(0, arrayPicker.arrayDepth),
                )}
                arrayLength={
                  arrayPicker.arrayLengths[arrayPicker.arrayDepth] ?? 0
                }
                indexLeafPath={buildJsonPath(arrayPicker.trail)}
                starLeafPath={buildJsonPath(
                  arrayPicker.trail.map((t, i) =>
                    i === arrayPicker.arrayDepth
                      ? ({ kind: "star" } as Token)
                      : t,
                  ),
                )}
                onApply={(sel) => {
                  const finalTrail =
                    sel === "star"
                      ? arrayPicker.trail.map((t, i) =>
                          i === arrayPicker.arrayDepth
                            ? ({ kind: "star" } as Token)
                            : t,
                        )
                      : arrayPicker.trail;
                  finalizePick(finalTrail, arrayPicker.leafValue);
                  setArrayPicker(null);
                }}
                onCancel={() => setArrayPicker(null)}
              />
            </div>
          )}
          {undoToast && (
            <div className="flex items-center gap-3 rounded border border-border bg-panel p-2 text-xs">
              <span>Path ersetzt durch Klick.</span>
              <button
                onClick={() => {
                  setPath(undoToast.previous.path);
                  setOp(undoToast.previous.op);
                  setNeedle(undoToast.previous.needle);
                  setUndoToast(null);
                }}
                className="rounded border border-border px-2 py-0.5 hover:border-border-strong"
              >
                Rückgängig
              </button>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <label className="font-medium">Mode</label>
            <select
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
                <label className="font-medium">Path</label>
                {mode === "jsonpath" ? (
                  <div className="w-56">
                    <PathSense
                      tree={pathTree}
                      value={path}
                      onChange={setPath}
                      onPick={(picked, sample) => {
                        setPath(picked);
                        const isScalar =
                          sample !== undefined &&
                          sample !== null &&
                          typeof sample !== "object";
                        if (isScalar) {
                          setOp("eq");
                          setNeedle(String(sample));
                        } else {
                          // object/array/null/undefined → use exists semantics
                          setOp("exists");
                          setNeedle("");
                        }
                      }}
                    />
                  </div>
                ) : (
                  <input
                    value={path}
                    onChange={(e) => setPath(e.target.value)}
                    placeholder="//order/@status"
                    className="w-56 rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 font-mono"
                  />
                )}
                <label className="font-medium">Op</label>
                <select
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
            <label className="font-medium">
              {mode === "contains"
                ? "Text"
                : mode === "js"
                  ? "JS-Ausdruck"
                  : "Wert"}
            </label>
            {mode === "js" ? (
              <textarea
                value={needle}
                onChange={(e) => setNeedle(e.target.value)}
                placeholder={'parsed && parsed.amount > 1000 && key.startsWith("ord-")'}
                className="min-h-[2.2rem] w-full flex-1 rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 font-mono"
                rows={2}
              />
            ) : (
              <input
                value={needle}
                onChange={(e) => setNeedle(e.target.value)}
                placeholder={
                  mode === "contains"
                    ? "Substring"
                    : op === "exists"
                      ? "(ignoriert)"
                      : "z.B. 42 / shipped / ^A.*"
                }
                className="w-56 rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 font-mono"
                disabled={mode !== "contains" && op === "exists"}
              />
            )}
          </div>
          {mode === "js" && (
            <div className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-2 text-[11px] text-[var(--color-text-muted)]">
              Variablen:{" "}
              <code className="font-mono">key</code>,{" "}
              <code className="font-mono">value</code> (string),{" "}
              <code className="font-mono">parsed</code> (JSON),{" "}
              <code className="font-mono">headers</code>,{" "}
              <code className="font-mono">partition</code>,{" "}
              <code className="font-mono">offset</code>,{" "}
              <code className="font-mono">timestampMs</code>. Beispiel:{" "}
              <code className="font-mono">
                parsed &amp;&amp; parsed.currency === "EUR" &amp;&amp;
                parsed.amount &gt; 500
              </code>
              . Limit 100 ms pro Nachricht.
            </div>
          )}

          <div className="flex flex-wrap items-center gap-2 text-xs">
            <label className="font-medium">Zeitraum</label>
            <select
              value={rangeMode}
              onChange={(e) => setRangeMode(e.target.value as typeof rangeMode)}
              className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
            >
              <option value="off">aus</option>
              <option value="preset">Preset</option>
              <option value="custom">Benutzerdefiniert</option>
            </select>
            {rangeMode === "preset" && (
              <div className="flex flex-wrap items-center gap-1">
                {(["5m", "15m", "1h", "6h", "24h", "7d", "30d", "today", "yesterday"] as const).map(
                  (p) => (
                    <button
                      key={p}
                      onClick={() => setPreset(p)}
                      className={`rounded border px-2 py-1 ${
                        preset === p
                          ? "border-accent bg-accent-subtle text-accent"
                          : "border-[var(--color-border)] bg-[var(--color-surface-raised)] hover:border-[var(--color-border-strong)]"
                      }`}
                    >
                      {p === "today" ? "Heute" : p === "yesterday" ? "Gestern" : `Letzte ${p}`}
                    </button>
                  ),
                )}
              </div>
            )}
            {rangeMode === "custom" && (
              <>
                <input
                  type="datetime-local"
                  value={customFrom}
                  onChange={(e) => setCustomFrom(e.target.value)}
                  className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
                />
                <span>→</span>
                <input
                  type="datetime-local"
                  value={customTo}
                  onChange={(e) => setCustomTo(e.target.value)}
                  className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
                />
              </>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-3 text-xs">
            <div className="flex items-center gap-1.5">
              <label className="font-medium">Richtung</label>
              <select
                value={direction}
                onChange={(e) => setDirection(e.target.value as SearchDirection)}
                className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
              >
                <option value="newest_first">neu → alt</option>
                <option value="oldest_first">alt → neu</option>
              </select>
            </div>
            <label className="flex items-center gap-1.5">
              <input
                type="checkbox"
                checked={stopOnLimit}
                onChange={(e) => setStopOnLimit(e.target.checked)}
                className="h-3.5 w-3.5"
              />
              Stop bei Limit
            </label>
            <div className="flex items-center gap-1.5">
              <label className="font-medium">Budget</label>
              <input
                type="number"
                min={100}
                max={500000}
                step={1000}
                value={budget}
                onChange={(e) => setBudget(Number(e.target.value) || 10000)}
                className="w-24 rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
              />
            </div>
            <button
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
            {searchResult && (
              <button
                onClick={clearSearch}
                className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 hover:border-[var(--color-border-strong)]"
              >
                Ergebnis löschen
              </button>
            )}
          </div>

          {searchError && (
            <div className="rounded border border-[var(--color-danger)]/30 bg-[var(--color-danger-subtle)] p-2 text-xs text-[var(--color-danger)]">
              {searchError}
            </div>
          )}
          {searchResult && (
            <div className="flex flex-wrap items-center gap-3 rounded border border-accent/30 bg-accent-subtle p-2 text-xs">
              <span className="font-semibold text-accent">
                {searchResult.stats.matched} matches
              </span>
              <span className="text-[var(--color-text-muted)]">
                · {searchResult.stats.scanned} gescannt
              </span>
              {searchResult.stats.parse_errors > 0 && (
                <span className="text-[var(--color-warning)]">
                  · {searchResult.stats.parse_errors} Parse-Fehler übersprungen
                </span>
              )}
              {searchResult.stats.budget_exhausted && (
                <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
                  Budget erschöpft
                </span>
              )}
              {searchResult.stats.durations_ms?.total !== undefined && (
                <span className="text-[var(--color-text-subtle)]">
                  · {searchResult.stats.durations_ms.total} ms
                </span>
              )}
              {searchResult.stats.next_cursors && (
                <button
                  onClick={() => {
                    const raw = searchResult.stats.next_cursors ?? {};
                    const cont: Record<string, number> = {};
                    for (const [k, v] of Object.entries(raw)) cont[k] = v as number;
                    runSearch(cont);
                  }}
                  disabled={searching}
                  className="ml-auto rounded border border-[var(--color-border)] px-2 py-1 hover:border-[var(--color-border-strong)]"
                >
                  Weiter suchen →
                </button>
              )}
            </div>
          )}
        </div>
      )}

      {msgsQuery.error && !searchResult && (
        <div className="m-3 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger-subtle)] p-3 text-sm text-[var(--color-danger)]">
          {(msgsQuery.error as Error).message}
        </div>
      )}

      {displayMessages.length === 0 && !searching && (
        <div className="p-8 text-center text-sm text-[var(--color-text-subtle)]">
          {searchResult ? "No matches." : "No messages."}
        </div>
      )}

      {showCoachmark && firstJsonIdx >= 0 && (
        <div className="m-3 flex items-center gap-2 rounded border border-accent/40 bg-accent-subtle p-2 text-xs text-accent">
          <span>Tipp: Klicke auf einen Wert in einer JSON-Nachricht, um danach zu filtern.</span>
          <button
            onClick={dismissCoachmark}
            className="ml-auto rounded border border-border px-2 py-0.5 hover:border-border-strong"
          >
            Verstanden
          </button>
        </div>
      )}

      <div className="divide-y divide-[var(--color-border)]">
        {displayMessages.map((m) => (
          <MessageRow
            key={`${m.partition}-${m.offset}`}
            m={m}
            onPick={handlePick}
          />
        ))}
      </div>

      {!searchResult && tailCursor && !live && (
        <div className="flex flex-col items-center gap-2 p-4">
          <button
            onClick={loadMore}
            disabled={loadingMore}
            className="rounded-md border border-[var(--color-border)] bg-[var(--color-panel)] px-4 py-2 text-sm font-medium text-[var(--color-text)] transition-colors hover:border-[var(--color-border-hover)] disabled:cursor-not-allowed disabled:opacity-50"
          >
            {loadingMore ? "Loading…" : "Load more"}
          </button>
          {loadMoreError && (
            <div className="text-xs text-[var(--color-danger)]">
              {loadMoreError}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ValueBody({
  m,
  onPick,
}: {
  m: Message;
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengths: number[],
  ) => void;
}) {
  const isJson = m.value_encoding === "json";
  if (isJson && m.value) {
    try {
      const parsed = JSON.parse(m.value);
      return (
        <div>
          <div className="mb-1 inline-flex items-center gap-1 rounded border border-border bg-accent-subtle px-1.5 py-0.5 text-[10px] text-muted">
            ⌕ click to filter
          </div>
          <JsonInteractive value={parsed} onPick={onPick} />
        </div>
      );
    } catch {
      // fall through to pretty()
    }
  }
  return (
    <pre className="overflow-auto text-xs">
      {pretty(m.value ?? "", m.value_encoding)}
    </pre>
  );
}

function MessageRow({
  m,
  onPick,
}: {
  m: Message;
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengths: number[],
  ) => void;
}) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const ts = new Date(m.timestamp_ms).toISOString().replace("T", " ").slice(0, 19);
  const preview =
    m.value_encoding === "null"
      ? "(null)"
      : m.value_encoding === "empty"
        ? "(empty)"
        : (m.value ?? "").slice(0, 160);

  const copyValue = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(m.value ?? "");
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore clipboard errors (permissions, insecure context)
    }
  };

  return (
    <div
      className="cursor-pointer px-4 py-2 text-xs transition-colors hover:bg-[var(--color-surface-hover)]"
      onClick={() => setOpen(!open)}
      role="button"
      tabIndex={0}
      aria-expanded={open}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          setOpen(!open);
        }
      }}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span
          className="font-mono text-[var(--color-text-subtle)]"
          aria-hidden="true"
        >
          {open ? "▾" : "▸"}
        </span>
        <span className="rounded bg-[var(--color-surface-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-text-muted)]">
          p{m.partition}
        </span>
        <span className="rounded bg-[var(--color-surface-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-text-muted)]">
          #{m.offset}
        </span>
        <EncodingBadge enc={m.value_encoding} />
        {m.value_sr && <SRBadge meta={m.value_sr} />}
        {m.masked && (
          <span
            title="Wert wurde durch eine data_masking-Regel verändert"
            className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-warning)]"
          >
            masked
          </span>
        )}
        <span className="text-[10px] text-[var(--color-text-subtle)]">{ts}</span>
        {m.key && (
          <span className="font-mono text-[var(--color-text-muted)]">
            <span className="text-[10px] uppercase tracking-wider text-[var(--color-text-subtle)]">key</span>{" "}
            {m.key.length > 40 ? m.key.slice(0, 40) + "…" : m.key}
          </span>
        )}
        <span className="flex-1 truncate font-mono text-[var(--color-text)]">{preview}</span>
      </div>
      {open && (
        <div className="mt-3 space-y-3" onClick={(e) => e.stopPropagation()}>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <DetailSection
              label={`key · ${m.key === undefined ? "none" : m.key_encoding}`}
              body={m.key === undefined ? "(no key)" : pretty(m.key, m.key_encoding)}
              empty={m.key === undefined}
            />
            <DetailSection
              label={`headers${m.headers ? ` · ${Object.keys(m.headers).length}` : ""}`}
              body={
                m.headers && Object.keys(m.headers).length > 0
                  ? Object.entries(m.headers)
                      .map(([k, v]) => `${k}: ${v}`)
                      .join("\n")
                  : "(no headers)"
              }
              empty={!m.headers || Object.keys(m.headers).length === 0}
            />
          </div>
          <DetailSection
            label={`value · ${m.value_encoding}${m.value_sr ? ` · sr id ${m.value_sr.schema_id ?? "?"}` : ""}`}
            body={<ValueBody m={m} onPick={onPick} />}
            action={
              <button
                onClick={copyValue}
                className="rounded border border-[var(--color-border)] px-2 py-1 text-[11px] hover:border-[var(--color-border-strong)]"
                title="Copy value to clipboard"
              >
                {copied ? "Copied!" : "Copy value"}
              </button>
            }
          />
        </div>
      )}
    </div>
  );
}

function DetailSection({
  label,
  body,
  empty,
  action,
}: {
  label: string;
  body: React.ReactNode;
  empty?: boolean;
  action?: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-2">
        <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-text-subtle)]">
          {label}
        </div>
        {action}
      </div>
      {typeof body === "string" ? (
        <pre
          className={
            "max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-[var(--color-surface-subtle)] p-3 font-mono text-[11px] leading-relaxed " +
            (empty
              ? "italic text-[var(--color-text-subtle)]"
              : "text-[var(--color-text)]")
          }
        >
          {body}
        </pre>
      ) : (
        <div
          className={
            empty
              ? "italic text-[var(--color-text-subtle)]"
              : "text-[var(--color-text)]"
          }
        >
          {body}
        </div>
      )}
    </div>
  );
}

function EncodingBadge({ enc }: { enc: string }) {
  const styles: Record<string, string> = {
    json: "bg-[var(--color-success-subtle)] text-[var(--color-success)]",
    text: "bg-[var(--color-surface-subtle)] text-[var(--color-text)]",
    binary: "bg-[var(--color-warning-subtle)] text-[var(--color-warning)]",
    null: "bg-[var(--color-surface-subtle)] text-[var(--color-text-subtle)]",
    empty: "bg-[var(--color-surface-subtle)] text-[var(--color-text-subtle)]",
    avro: "bg-[var(--color-info-subtle)] text-[var(--color-info)]",
    protobuf: "bg-[var(--color-info-subtle)] text-[var(--color-info)]",
    json_schema: "bg-[var(--color-info-subtle)] text-[var(--color-info)]",
  };
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${styles[enc] || "bg-[var(--color-surface-subtle)] text-[var(--color-text)]"}`}
    >
      {enc}
    </span>
  );
}

function SRBadge({ meta }: { meta: { format?: string; schema_id?: number; subject?: string; version?: number } }) {
  const label = meta.subject
    ? `${meta.subject}${meta.version ? `:v${meta.version}` : ""}`
    : meta.schema_id
      ? `id ${meta.schema_id}`
      : "schema";
  const title = `Schema Registry · format=${meta.format ?? "?"} · id=${meta.schema_id ?? "?"}${
    meta.subject ? ` · subject=${meta.subject}` : ""
  }${meta.version ? ` · version=${meta.version}` : ""}`;
  return (
    <span
      title={title}
      className="rounded bg-[var(--color-info-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-info)]"
    >
      sr · {label}
    </span>
  );
}

// Single-section body formatter removed — the row now renders key/value/headers
// as individual DetailSection blocks for better readability and per-field copy.



function pretty(s: string, enc: string): string {
  if (enc === "json") {
    try {
      return JSON.stringify(JSON.parse(s), null, 2);
    } catch {
      return s;
    }
  }
  return s;
}
