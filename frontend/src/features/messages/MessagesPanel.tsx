import { useMemo, useState } from "react";
import type { PartitionInfo } from "@/lib/api";
import { useFormatters } from "@/lib/use-formatters";
import { StatusBox, StatusIcon } from "@/components/ui/StatusIcon";
import { SearchPanel } from "./SearchPanel";
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
  const search = useMessageSearch(cluster, topic, partition, limit, form);
  const { searching, searchResult, inSearchMode } = search;

  const suggestions = usePathSuggestions(cluster, topic, form.mode, searchOpen);

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
        <SearchPanel
          form={form}
          suggestions={suggestions}
          search={search}
          limit={limit}
          showUndo={showUndo}
          onUndo={undo}
        />
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
