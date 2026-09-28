import { useMemo } from "react";
import type { PartitionInfo } from "@/lib/api";
import { useFormatters } from "@/lib/use-formatters";
import { clampOffset, offsetBoundsFor, type BrowseFrom } from "./browse-params";
import { useNumberDraft } from "./use-number-draft";

/** "From" select plus, for `offset`, the start-offset input and its valid range. */
export function BrowseFromControl({
  partitions,
  partition,
  from,
  msgOffset,
  onFromChange,
  onMsgOffsetChange,
  disabled,
}: {
  partitions: PartitionInfo[];
  partition: number;
  from: BrowseFrom;
  msgOffset: number;
  onFromChange: (v: BrowseFrom) => void;
  onMsgOffsetChange: (v: number) => void;
  disabled: boolean;
}) {
  const fmt = useFormatters();
  const offsetBounds = useMemo(
    () => offsetBoundsFor(partitions, partition),
    [partition, partitions],
  );

  // Local draft so typing an offset does not refetch on every keystroke; the
  // value is committed (and clamped to the valid range) on Enter or blur.
  const {
    draft: offsetDraft,
    setDraft: setOffsetDraft,
    commit: commitOffset,
  } = useNumberDraft(msgOffset, (raw) => clampOffset(raw, offsetBounds), onMsgOffsetChange);

  return (
    <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
      <label htmlFor="browse-from">From</label>
      <select
        id="browse-from"
        value={from}
        onChange={(e) => onFromChange(e.target.value as BrowseFrom)}
        className="rounded border border-[var(--color-border)] px-2 py-1 text-xs"
        disabled={disabled}
      >
        <option value="end">latest</option>
        <option value="start">oldest</option>
        <option value="offset">offset</option>
      </select>
      {from === "offset" && (
        <>
          <input
            aria-label="Start offset"
            value={offsetDraft}
            onChange={(e) => setOffsetDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                commitOffset();
              }
            }}
            onBlur={commitOffset}
            inputMode="numeric"
            className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-xs font-mono"
            placeholder="0"
            disabled={disabled}
            title={
              offsetBounds
                ? partition < 0
                  ? `Seeks every partition to this offset (valid ${fmt.number(offsetBounds.min)}–${fmt.number(offsetBounds.max)}). Press Enter to apply.`
                  : `Valid ${fmt.number(offsetBounds.min)}–${fmt.number(offsetBounds.max)}. Press Enter to apply.`
                : "Press Enter to apply."
            }
          />
          {offsetBounds && (
            <span className="text-[var(--color-text-subtle)]">
              {partition < 0 ? "all · " : ""}
              {fmt.number(offsetBounds.min)}–{fmt.number(offsetBounds.max)}
            </span>
          )}
        </>
      )}
    </div>
  );
}
