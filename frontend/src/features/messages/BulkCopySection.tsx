import { BulkCopyPanel } from "./BulkCopyPanel";

/** Collapsible "copy messages to another cluster / topic" section. */
export function BulkCopySection({
  cluster,
  topic,
  partitions,
  open,
  onToggle,
}: {
  cluster: string;
  topic: string;
  partitions: number[];
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-sm">
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center justify-between px-4 py-3 text-left text-sm font-semibold text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
      >
        <span>Copy messages to another cluster / topic</span>
        <span className="text-[var(--color-text-subtle)]">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="border-t border-[var(--color-border)] p-4">
          <BulkCopyPanel srcCluster={cluster} srcTopic={topic} partitions={partitions} />
        </div>
      )}
    </div>
  );
}
