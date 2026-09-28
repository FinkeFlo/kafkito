import { useMemo, useState } from "react";
import type { PartitionInfo, SearchMode, SearchOp, SearchDirection } from "@/lib/api";
import { MAX_HYDRATE_VALUE_BYTES } from "@/lib/hydrate-sample";
import { useFormatters } from "@/lib/use-formatters";
import { StatusBox, StatusIcon } from "@/components/ui/StatusIcon";
import { PathSense } from "./PathSense";
import { MessageRow } from "./MessageRow";
import { BrowseToolbar } from "./BrowseToolbar";
import { computeTimeRange } from "./time-range";
import { orderForDisplay, type SortOrder } from "./display-order";
import { useMessagesSearchParams } from "./use-messages-search-params";
import { useTimeRangeState } from "./use-time-range-state";
import { useBrowseMessages } from "./use-browse-messages";
import { useLoadMore } from "./use-load-more";
import { useSearchForm } from "./use-search-form";
import { useMessageSearch } from "./use-message-search";
import { usePathSuggestions } from "./use-path-suggestions";
import { useClickToFilter } from "./use-click-to-filter";
import { useJsonCoachmark } from "./use-json-coachmark";

/** Message browser of a topic: browse filters, search, and the message list. */
export function MessagesPanel({
  cluster,
  topic,
  partitions,
}: {
  cluster: string;
  topic: string;
  partitions: PartitionInfo[];
}) {
  const fmt = useFormatters();
  const searchParams = useMessagesSearchParams();
  const { partition, limit, from, msgOffset } = searchParams;
  const [live, setLive] = useState<boolean>(false);
  const [sortOrder, setSortOrder] = useState<SortOrder>("newest");

  // Browse-level time-range filter (separate state from the search panel below)
  const browseRangeState = useTimeRangeState();
  const {
    mode: browseRangeMode,
    preset: browsePreset,
    customFrom: browseCustomFrom,
    customTo: browseCustomTo,
  } = browseRangeState;

  // Search state
  const [searchOpen, setSearchOpen] = useState(false);
  const form = useSearchForm();
  const {
    mode,
    setMode,
    path,
    setPath,
    op,
    setOp,
    needle,
    setNeedle,
    direction,
    setDirection,
    stopOnLimit,
    setStopOnLimit,
    budget,
    setBudget,
    budgetUnlimited,
    setBudgetUnlimited,
  } = form;
  const {
    mode: rangeMode,
    setMode: setRangeMode,
    preset,
    setPreset,
    customFrom,
    setCustomFrom,
    customTo,
    setCustomTo,
  } = form.range;
  const {
    searching,
    searchResult,
    searchError,
    searchStopReason,
    inSearchMode,
    runSearch,
    stopSearch,
    clearSearch,
  } = useMessageSearch(cluster, topic, partition, limit, form);

  const { pathTree, xmlPathTree, sampleTooLargeToScan } = usePathSuggestions(
    cluster,
    topic,
    mode,
    searchOpen,
  );

  const { showUndo, handlePick, undo } = useClickToFilter(form, () => setSearchOpen(true));

  const browseRange = useMemo(
    () => computeTimeRange(browseRangeMode, browsePreset, browseCustomFrom, browseCustomTo),
    [browseRangeMode, browsePreset, browseCustomFrom, browseCustomTo],
  );

  const { params, query: msgsQuery } = useBrowseMessages({
    cluster,
    topic,
    partitions,
    partition,
    limit,
    from,
    msgOffset,
    range: browseRange,
    live,
    paused: !!searchResult,
  });

  const { tailMessages, tailCursor, loadingMore, loadMoreError, loadMore } = useLoadMore(
    cluster,
    topic,
    params,
    msgsQuery.data,
  );

  const rawMessages = inSearchMode
    ? (searchResult?.messages ?? [])
    : [...(msgsQuery.data?.messages ?? []), ...tailMessages];
  const displayMessages = useMemo(
    () => orderForDisplay(rawMessages, sortOrder),
    [rawMessages, sortOrder],
  );

  const coachmark = useJsonCoachmark(displayMessages);

  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-sm">
      <BrowseToolbar
        cluster={cluster}
        topic={topic}
        partitions={partitions}
        searchParams={searchParams}
        range={browseRangeState}
        resolvedRange={browseRange}
        live={live}
        onLiveChange={setLive}
        sortOrder={sortOrder}
        onSortOrderChange={setSortOrder}
        searchOpen={searchOpen}
        onToggleSearch={() => setSearchOpen((v) => !v)}
        onRefresh={() => msgsQuery.refetch()}
        locked={!!searchResult}
        count={{
          inSearchMode,
          searching,
          matched: searchResult?.stats.matched ?? 0,
          scanned: searchResult?.stats.scanned ?? 0,
          shown: displayMessages.length,
          fetching: msgsQuery.isFetching,
        }}
      />

      {searchOpen && (
        <div className="space-y-3 border-b border-[var(--color-border)] bg-[var(--color-surface-subtle)] p-3">
          {showUndo && (
            <div className="flex items-center gap-3 rounded border border-border bg-panel p-2 text-xs">
              <span>Path replaced by click.</span>
              <button
                type="button"
                onClick={undo}
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

      {coachmark.visible && (
        <div className="m-3 flex items-center gap-2 rounded border border-accent/40 bg-accent-subtle p-2 text-xs text-accent">
          <span>Tip: click any value in a JSON message to filter by it.</span>
          <button
            type="button"
            onClick={coachmark.dismiss}
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
