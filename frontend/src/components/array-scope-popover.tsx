import { useEffect, useState } from "react";

export type ArrayScopeSelection = "index" | "star";

export interface ArrayScopePopoverProps {
  arrayPath: string;
  arrayLength: number;
  indexLeafPath: string;
  starLeafPath: string;
  onApply: (sel: ArrayScopeSelection) => void;
  onCancel: () => void;
}

export function ArrayScopePopover({
  arrayPath,
  arrayLength,
  indexLeafPath,
  starLeafPath,
  onApply,
  onCancel,
}: ArrayScopePopoverProps) {
  const [sel, setSel] = useState<ArrayScopeSelection>("star");

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
      if (e.key === "Enter") onApply(sel);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [sel, onApply, onCancel]);

  return (
    <div
      role="dialog"
      aria-label="Array-Scope"
      className="rounded-md border border-border bg-panel shadow-md p-3 text-sm"
    >
      <div className="mb-2 text-muted">
        <code className="font-mono">{arrayPath}</code> ist ein Array ({arrayLength} Einträge im Sample)
      </div>
      <fieldset className="space-y-1.5">
        <legend className="sr-only">Treffer in</legend>
        <label className="flex items-start gap-2">
          <input
            type="radio"
            name="scope"
            checked={sel === "index"}
            onChange={() => setSel("index")}
          />
          <span>
            <span className="font-medium">Diesem Index</span>{" "}
            <span className="font-mono text-muted">{indexLeafPath}</span>
          </span>
        </label>
        <label className="flex items-start gap-2">
          <input
            type="radio"
            name="scope"
            checked={sel === "star"}
            onChange={() => setSel("star")}
          />
          <span>
            <span className="font-medium">Allen Einträgen</span>{" "}
            <span className="font-mono text-muted">{starLeafPath}</span>
          </span>
        </label>
      </fieldset>
      <div className="mt-3 flex justify-end gap-2">
        <button
          onClick={onCancel}
          className="rounded border border-border px-2 py-1 hover:border-border-strong"
        >
          Cancel
        </button>
        <button
          onClick={() => onApply(sel)}
          className="rounded bg-accent px-3 py-1 font-semibold text-[var(--color-text-on-accent)] hover:bg-accent-hover"
        >
          Übernehmen
        </button>
      </div>
    </div>
  );
}
