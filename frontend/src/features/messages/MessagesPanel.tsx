import { useMemo, useState } from "react";
import type { PartitionInfo } from "@/lib/api";
import { BrowseToolbar } from "./BrowseToolbar";
import { JsonCoachmark } from "./JsonCoachmark";
import { LoadMoreFooter } from "./LoadMoreFooter";
import { MessageList } from "./MessageList";
import { MessageListStatus } from "./MessageListStatus";
import { SearchPanel } from "./SearchPanel";
import { orderForDisplay, type SortOrder } from "./display-order";
import { computeTimeRange } from "./time-range";
import { useBrowseMessages } from "./use-browse-messages";
import { useClickToFilter } from "./use-click-to-filter";
import { useJsonCoachmark } from "./use-json-coachmark";
import { useLoadMore } from "./use-load-more";
import { useMessageSearch } from "./use-message-search";
import { useMessagesSearchParams } from "./use-messages-search-params";
import { usePathSuggestions } from "./use-path-suggestions";
import { useSearchForm } from "./use-search-form";
import { useTimeRangeState } from "./use-time-range-state";

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
  const form = useSearchForm(topic);
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

  const browseMessages = [...(msgsQuery.data?.messages ?? []), ...tailMessages];
  const rawMessages = inSearchMode ? (searchResult?.messages ?? []) : browseMessages;
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
        onToggleSearch={() => {
          // Decided from the messages already on screen, so the panel opens
          // in its final mode instead of switching once a sample arrives.
          if (!searchOpen) form.preselectMode(browseMessages);
          setSearchOpen((v) => !v);
        }}
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

      <MessageListStatus
        error={msgsQuery.error}
        partial={!!msgsQuery.data?.partial}
        hasResult={!!searchResult}
        empty={displayMessages.length === 0}
        searching={searching}
        scanned={searchResult?.stats.scanned ?? 0}
        loading={msgsQuery.isLoading}
      />

      {coachmark.visible && <JsonCoachmark onDismiss={coachmark.dismiss} />}

      <MessageList messages={displayMessages} onPick={handlePick} />

      {!inSearchMode && tailCursor && !live && (
        <LoadMoreFooter loading={loadingMore} error={loadMoreError} onLoadMore={loadMore} />
      )}
    </div>
  );
}
