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
    <div className="rounded-lg border border-border bg-panel shadow-sm">
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center justify-between px-4 py-3 text-left text-sm font-semibold text-text hover:bg-hover"
      >
        <span>Copy messages to another cluster / topic</span>
        <span className="text-subtle-text">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="border-t border-border p-4">
          <BulkCopyPanel srcCluster={cluster} srcTopic={topic} partitions={partitions} />
        </div>
      )}
    </div>
  );
}
