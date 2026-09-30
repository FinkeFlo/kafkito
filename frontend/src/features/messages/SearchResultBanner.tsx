import { useFormatters } from "@/lib/use-formatters";
import type { SearchResult, SearchStopReason } from "./search-chain";

/** Match and scan counts of a search, why it stopped, and "Search more". */
export function SearchResultBanner({
  result,
  searching,
  stopReason,
  onSearchMore,
}: {
  result: SearchResult;
  searching: boolean;
  stopReason: SearchStopReason | null;
  onSearchMore: () => void;
}) {
  const fmt = useFormatters();
  const { stats } = result;

  return (
    <div className="flex flex-wrap items-center gap-3 rounded border border-accent/30 bg-accent-subtle p-2 text-xs">
      <span className="font-semibold text-accent">{fmt.number(stats.matched)} matches</span>
      <span className="text-[var(--color-text-muted)]">
        · {fmt.number(stats.scanned)} scanned
        {searching && " …"}
      </span>
      {stats.parse_errors > 0 && (
        <span
          className="text-[var(--color-warning)] cursor-default"
          title={
            stats.parse_error_offsets && stats.parse_error_offsets.length > 0
              ? stats.parse_error_offsets
                  .map((e) => `p${e.partition}@${e.offset}: ${e.error}`)
                  .join("\n")
              : undefined
          }
        >
          · {fmt.number(stats.parse_errors)} parse errors skipped
        </span>
      )}
      {!searching && stopReason === "budget" && (
        <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
          Scan limit reached
        </span>
      )}
      {!searching && stopReason === "limit" && (
        <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
          Limit reached
        </span>
      )}
      {!searching && stopReason === "stopped" && (
        <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
          Stopped
        </span>
      )}
      {!searching && stopReason === "timeout" && (
        <span className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[var(--color-warning)]">
          {stats.timed_out ? "Timed out" : "No progress"}
        </span>
      )}
      {!searching && stopReason === "complete" && !stats.more_available && !stats.timed_out && (
        <span className="rounded bg-[var(--color-success-subtle)] px-1.5 py-0.5 text-[var(--color-success)]">
          Range fully scanned
        </span>
      )}
      {!searching && (
        <span className="ml-auto flex items-center gap-3">
          {stats.more_available && (
            <button
              type="button"
              onClick={onSearchMore}
              className="rounded border border-[var(--color-border)] px-2 py-1 hover:border-[var(--color-border-strong)]"
            >
              Search more →
            </button>
          )}
        </span>
      )}
    </div>
  );
}
