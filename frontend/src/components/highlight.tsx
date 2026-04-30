import { type ReactNode } from "react";
import type { HighlightRange } from "@/lib/fuzzy";

export interface HighlightProps {
  text: string;
  /** Fuse.js-style indices: inclusive `[start, end]` character offsets. */
  ranges: readonly HighlightRange[];
  className?: string;
}

/**
 * Renders `text` with the given character ranges wrapped in a `<mark>`.
 * Designed for short Kafka identifiers; overlapping ranges are merged.
 */
export function Highlight({ text, ranges, className }: HighlightProps): ReactNode {
  if (!ranges.length) return <span className={className}>{text}</span>;
  const merged = mergeRanges(ranges);
  const out: ReactNode[] = [];
  let cursor = 0;
  merged.forEach(([start, end], i) => {
    if (start > cursor) out.push(text.slice(cursor, start));
    out.push(
      <mark
        key={i}
        className="rounded-[2px] bg-accent-subtle px-0.5 text-accent"
      >
        {text.slice(start, end + 1)}
      </mark>,
    );
    cursor = end + 1;
  });
  if (cursor < text.length) out.push(text.slice(cursor));
  return <span className={className}>{out}</span>;
}

function mergeRanges(ranges: readonly HighlightRange[]): HighlightRange[] {
  const sorted = [...ranges].sort((a, b) => a[0] - b[0]);
  const out: [number, number][] = [];
  for (const [s, e] of sorted) {
    const last = out[out.length - 1];
    if (last && s <= last[1] + 1) last[1] = Math.max(last[1], e);
    else out.push([s, e]);
  }
  return out;
}
