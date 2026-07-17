import { useEffect, useMemo, useRef, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { fetchMessageCount } from "@/lib/api";
import { formatNumber } from "@/lib/format";

export const MESSAGE_RANGE_COUNT_DEBOUNCE_MS = 400;

export function MessageRangeCountPreview({
  cluster,
  topic,
  partition,
  from_ts_ms,
  to_ts_ms,
  live,
}: {
  cluster: string;
  topic: string;
  partition: number;
  from_ts_ms?: number;
  to_ts_ms?: number;
  live: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const request = useMemo(
    () => ({
      partition,
      from_ts_ms,
      to_ts_ms,
    }),
    [partition, from_ts_ms, to_ts_ms],
  );
  const [debouncedRequest, setDebouncedRequest] = useState(request);

  useEffect(() => {
    const timer = setTimeout(
      () => setDebouncedRequest(request),
      MESSAGE_RANGE_COUNT_DEBOUNCE_MS,
    );
    return () => clearTimeout(timer);
  }, [request]);

  useEffect(() => {
    if (!expanded) return;
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setExpanded(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [expanded]);

  const query = useQuery({
    queryKey: ["message-count", cluster, topic, debouncedRequest],
    queryFn: () => fetchMessageCount(cluster, topic, debouncedRequest),
    enabled: !live,
    placeholderData: keepPreviousData,
    staleTime: 10_000,
    retry: false,
  });

  const total = query.data?.total_approx_count ?? null;
  const rows = query.data?.partitions ?? [];
  // Only offer the per-partition breakdown when it adds information,
  // i.e. when the range spans more than one partition.
  const hasBreakdown = rows.length > 1;

  let label: string;
  let labelClass = "font-semibold text-text";
  if (query.isError) {
    label = "count failed";
    labelClass = "font-semibold text-danger";
  } else if (live) {
    label =
      total !== null ? `≈ ${formatNumber(total)} messages` : "snapshot paused";
  } else if (total === null) {
    label = "≈ … messages";
  } else {
    label = `≈ ${formatNumber(total)} messages`;
  }

  const tooltip = query.isError
    ? (query.error as Error).message
    : live
      ? "Snapshot paused while Live is on. Turn Live off to refresh the range count."
      : "Approximate message count for the selected range. Live traffic may shift this slightly.";

  return (
    <div
      ref={wrapRef}
      className="relative flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]"
    >
      <span className={labelClass} title={tooltip}>
        {label}
      </span>
      {live && (
        <span className="rounded-sm bg-panel px-1 py-0.5 text-[9px] font-semibold uppercase tracking-wider text-muted">
          snap
        </span>
      )}
      {!live && query.isFetching && (
        <span aria-label="updating" title="updating" className="text-[10px] text-subtle-text">
          …
        </span>
      )}
      {!live && hasBreakdown && (
        <button
          type="button"
          onClick={() => setExpanded((open) => !open)}
          aria-expanded={expanded}
          aria-label="Per partition"
          className="text-muted transition-colors hover:text-text"
          title="Per-partition breakdown"
        >
          {expanded ? "▾" : "▸"}
        </button>
      )}

      {expanded && hasBreakdown && (
        <div className="absolute left-0 top-full z-20 mt-1 min-w-[15rem] rounded-md border border-border bg-subtle p-2 shadow-lg">
          <div className="overflow-x-auto">
            <table className="w-full font-mono text-xs">
              <thead className="text-[10px] uppercase tracking-wider text-subtle-text">
                <tr>
                  <th className="text-left">partition</th>
                  <th className="text-right">from</th>
                  <th className="pl-3 text-right">to</th>
                  <th className="pl-3 text-right">≈ count</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.partition}>
                    <td className="text-accent">p{row.partition}</td>
                    <td className="text-right text-muted">
                      {formatNumber(row.from_offset)}
                    </td>
                    <td className="pl-3 text-right text-muted">
                      {formatNumber(row.to_offset)}
                    </td>
                    <td className="pl-3 text-right">
                      {formatNumber(row.approx_count)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mt-1 text-[10px] text-subtle-text">
            Approximate at preview time. Live traffic may shift this slightly.
          </p>
        </div>
      )}
    </div>
  );
}
