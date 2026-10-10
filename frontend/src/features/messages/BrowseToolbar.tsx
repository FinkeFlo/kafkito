import type { PartitionInfo } from "@/lib/api";
import { useFormatters } from "@/lib/use-formatters";
import { BrowseFromControl } from "./BrowseFromControl";
import { MessageRangeCountPreview } from "./MessageRangeCountPreview";
import { RangePicker } from "./RangePicker";
import { clampLimit } from "./browse-params";
import type { SortOrder } from "./display-order";
import type { TimeRange } from "./time-range";
import type { MessagesSearchParams } from "./use-messages-search-params";
import { useNumberDraft } from "./use-number-draft";
import type { TimeRangeState } from "./use-time-range-state";

type CountStatus = {
  inSearchMode: boolean;
  searching: boolean;
  /** Matches so far (search) … */
  matched: number;
  scanned: number;
  /** … or rows on screen (browse). */
  shown: number;
  fetching: boolean;
};

/** Browse filters, the search toggle, Refresh, and the message count. */
export function BrowseToolbar({
  cluster,
  topic,
  partitions,
  searchParams,
  range,
  resolvedRange,
  live,
  onLiveChange,
  sortOrder,
  onSortOrderChange,
  searchOpen,
  onToggleSearch,
  onRefresh,
  locked,
  count,
}: {
  cluster: string;
  topic: string;
  partitions: PartitionInfo[];
  searchParams: MessagesSearchParams;
  range: TimeRangeState;
  resolvedRange: TimeRange;
  live: boolean;
  onLiveChange: (v: boolean) => void;
  sortOrder: SortOrder;
  onSortOrderChange: (v: SortOrder) => void;
  searchOpen: boolean;
  onToggleSearch: () => void;
  onRefresh: () => void;
  /** A search result is on screen: controls that only affect browsing are off. */
  locked: boolean;
  count: CountStatus;
}) {
  const fmt = useFormatters();
  const { partition, limit, from, msgOffset, setPartition, setLimit, setFrom, setMsgOffset } =
    searchParams;

  const {
    draft: limitDraft,
    setDraft: setLimitDraft,
    commit: commitLimit,
  } = useNumberDraft(limit, clampLimit, setLimit);

  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-border p-3">
      <div className="flex items-center gap-1.5 text-xs text-muted">
        <label htmlFor="browse-partition">Partition</label>
        <select
          id="browse-partition"
          value={partition}
          onChange={(e) => setPartition(Number(e.target.value))}
          className="rounded border border-border px-2 py-1 text-xs"
        >
          <option value={-1}>all</option>
          {partitions.map((p) => (
            <option key={p.partition} value={p.partition}>
              {p.partition}
            </option>
          ))}
        </select>
      </div>
      <BrowseFromControl
        partitions={partitions}
        partition={partition}
        from={from}
        msgOffset={msgOffset}
        onFromChange={setFrom}
        onMsgOffsetChange={setMsgOffset}
        disabled={locked}
      />
      <div className="flex items-center gap-1.5 text-xs text-muted">
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
          className="w-20 rounded border border-border px-2 py-1 text-xs"
        />
      </div>
      <fieldset
        aria-labelledby="browse-range"
        className="flex min-w-0 items-center gap-1.5 text-xs text-muted"
      >
        <span id="browse-range">Range</span>
        <RangePicker
          mode={range.mode}
          preset={range.preset}
          customFrom={range.customFrom}
          customTo={range.customTo}
          onChange={(m, p, f, t) => {
            range.setMode(m);
            range.setPreset(p);
            range.setCustomFrom(f);
            range.setCustomTo(t);
          }}
          disabled={locked}
        />
      </fieldset>
      <MessageRangeCountPreview
        cluster={cluster}
        topic={topic}
        partition={partition}
        from_ts_ms={resolvedRange.from_ts_ms}
        to_ts_ms={resolvedRange.to_ts_ms}
        live={live}
      />
      <div className="flex items-center gap-1.5 text-xs text-muted">
        <label htmlFor="browse-sort">Sort</label>
        <select
          id="browse-sort"
          value={sortOrder}
          onChange={(e) => onSortOrderChange(e.target.value as SortOrder)}
          className="rounded border border-border px-2 py-1 text-xs"
          title="Order of displayed messages"
        >
          <option value="newest">newest first</option>
          <option value="oldest">oldest first</option>
        </select>
      </div>
      <label className="flex items-center gap-1.5 text-xs text-muted">
        <input
          type="checkbox"
          checked={live}
          onChange={(e) => onLiveChange(e.target.checked)}
          className="h-3.5 w-3.5"
          disabled={locked}
        />
        Live
      </label>
      <button
        type="button"
        onClick={onToggleSearch}
        className={`rounded border px-2 py-1 text-xs ${
          searchOpen
            ? "border-accent bg-accent-subtle text-accent"
            : "border-border hover:border-border-hover"
        }`}
      >
        {searchOpen ? "Close search" : "Search"}
      </button>
      <button
        type="button"
        onClick={onRefresh}
        className="ml-auto rounded border border-border px-2 py-1 text-xs hover:border-border-hover"
        disabled={locked}
      >
        Refresh
      </button>
      <span data-testid="messages-count" className="text-xs text-muted">
        {count.inSearchMode ? fmt.number(count.matched) : fmt.number(count.shown)}
        {!count.inSearchMode && count.fetching && " · fetching…"}
        {count.searching && ` · ${fmt.number(count.scanned)} scanned · searching…`}
      </span>
    </div>
  );
}
