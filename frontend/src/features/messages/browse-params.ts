import type { PartitionInfo } from "@/lib/api";

export type BrowseFrom = "end" | "start" | "offset";

export type OffsetBounds = { min: number; max: number };

/**
 * Valid offset range for the current partition selection (single partition
 * or all). Used to hint the input and clamp committed values.
 */
export function offsetBoundsFor(
  partitions: PartitionInfo[],
  partition: number,
): OffsetBounds | null {
  const sel = partition >= 0 ? partitions.filter((p) => p.partition === partition) : partitions;
  if (sel.length === 0) return null;
  const min = Math.min(...sel.map((p) => p.start_offset));
  const maxEnd = Math.max(...sel.map((p) => p.end_offset));
  return { min, max: Math.max(min, maxEnd - 1) };
}

/** Parses a typed start offset: non-negative integer, clamped to `bounds`. */
export function clampOffset(raw: string, bounds: OffsetBounds | null): number {
  const parsed = Number(raw);
  let next = Number.isFinite(parsed) && parsed >= 0 ? Math.floor(parsed) : 0;
  if (bounds) {
    next = Math.min(Math.max(next, bounds.min), bounds.max);
  }
  return next;
}

/** Parses a typed page size: 1–500, falling back to 50. */
export function clampLimit(raw: string): number {
  const parsed = Number(raw);
  let next = Number.isFinite(parsed) && parsed >= 1 ? Math.floor(parsed) : 50;
  if (next > 500) next = 500;
  return next;
}
