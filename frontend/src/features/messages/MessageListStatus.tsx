import { StatusBox } from "@/components/ui/StatusIcon";
import { useFormatters } from "@/lib/use-formatters";

/** Error, partial-page, searching, loading and empty notices above the list. */
export function MessageListStatus({
  error,
  partial,
  hasResult,
  empty,
  searching,
  scanned,
  loading,
}: {
  /** Error of the browse head page. */
  error: Error | null;
  /** The head page may be missing its newest records. */
  partial: boolean;
  /** A search result is on screen. */
  hasResult: boolean;
  empty: boolean;
  searching: boolean;
  scanned: number;
  loading: boolean;
}) {
  const fmt = useFormatters();

  return (
    <>
      {error && !hasResult && (
        <StatusBox intent="danger" className="m-3 p-3 text-sm">
          {error.message}
        </StatusBox>
      )}

      {!error && !hasResult && partial && (
        <StatusBox intent="warning" className="m-3 p-3 text-sm">
          This page may be incomplete — a very large record delayed loading past the server's
          timeout, so the newest message(s) might be missing. Try Refresh.
        </StatusBox>
      )}

      {empty && searching && (
        <div className="p-8 text-center text-sm text-subtle-text">
          Searching… {fmt.number(scanned)} scanned, no match yet.
        </div>
      )}

      {empty && !searching && loading && (
        <div className="p-8 text-center text-sm text-subtle-text">Loading messages…</div>
      )}

      {/* `isLoading` (not `isPending`) is the right guard: the query is
          disabled while a search result is on screen, and a disabled query
          stays `pending` forever — which would hide the "No matches." state. */}
      {empty && !searching && !loading && (
        <div className="p-8 text-center text-sm text-subtle-text">
          {hasResult ? "No matches." : "No messages."}
        </div>
      )}
    </>
  );
}
