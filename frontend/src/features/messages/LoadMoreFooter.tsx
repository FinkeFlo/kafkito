import { StatusIcon } from "@/components/ui/StatusIcon";

/** "Load more" button below the list, with the last failure if any. */
export function LoadMoreFooter({
  loading,
  error,
  onLoadMore,
}: {
  loading: boolean;
  error: string | null;
  onLoadMore: () => void;
}) {
  return (
    <div className="flex flex-col items-center gap-2 p-4">
      <button
        type="button"
        onClick={onLoadMore}
        disabled={loading}
        className="rounded-md border border-[var(--color-border)] bg-[var(--color-panel)] px-4 py-2 text-sm font-medium text-[var(--color-text)] transition-colors hover:border-[var(--color-border-hover)] disabled:cursor-not-allowed disabled:opacity-50"
      >
        {loading ? "Loading…" : "Load more"}
      </button>
      {error && (
        <div className="flex items-center gap-1 text-xs text-[var(--color-danger)]">
          <StatusIcon intent="danger" />
          {error}
        </div>
      )}
    </div>
  );
}
