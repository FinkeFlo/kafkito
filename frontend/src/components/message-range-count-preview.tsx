import { useEffect, useMemo, useState } from "react";
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
  const hasBreakdown = rows.length > 0;

  return (
    <div className="min-w-[15rem] rounded-md border border-border bg-subtle p-2 text-xs">
      <div className="flex items-center gap-2">
        <span className="font-semibold text-text">
          {total !== null
            ? `≈ ${formatNumber(total)} messages`
            : live
              ? "Range snapshot paused"
              : "Calculating range…"}
        </span>
        {live && (
          <span className="rounded-sm bg-panel px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-muted">
            snapshot
          </span>
        )}
        {!live && query.isFetching && (
          <span className="ml-auto text-[10px] text-subtle-text">updating…</span>
        )}
      </div>

      {query.isError ? (
        <p className="mt-1 text-danger">{(query.error as Error).message}</p>
      ) : live ? (
        <p className="mt-1 text-subtle-text">
          Paused while Live is on. Refresh the snapshot by turning Live off.
        </p>
      ) : total === null ? (
        <p className="mt-1 text-subtle-text">
          Resolving start and end offsets for this range.
        </p>
      ) : (
        <>
          {hasBreakdown && (
            <button
              type="button"
              onClick={() => setExpanded((open) => !open)}
              className="mt-1 text-muted transition-colors hover:text-text"
            >
              {expanded ? "Hide partitions ▾" : "Per partition ▸"}
            </button>
          )}
          {expanded && (
            <div className="mt-2 overflow-x-auto">
              <table className="w-full font-mono">
                <thead className="text-[10px] uppercase tracking-wider text-subtle-text">
                  <tr>
                    <th className="text-left">partition</th>
                    <th className="text-right">from</th>
                    <th className="text-right pl-3">to</th>
                    <th className="text-right pl-3">≈ count</th>
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
          )}
          <p className="mt-1 text-[10px] text-subtle-text">
            Approximate at preview time. Live traffic may shift this slightly.
          </p>
        </>
      )}
    </div>
  );
}
