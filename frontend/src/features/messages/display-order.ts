import { dedupeMessages } from "@/lib/dedupe-messages";

export type SortOrder = "newest" | "oldest";

type Ordered = { partition: number; offset: number; timestamp_ms: number };

/** Orders the list for display and drops records that appear twice. */
export function orderForDisplay<T extends Ordered>(messages: T[], sortOrder: SortOrder): T[] {
  if (sortOrder === "oldest") return dedupeMessages(messages);
  // Stable sort: newest timestamp first; ties broken by (partition, offset) desc
  // so concurrent records keep a deterministic order.
  // Dedupe after sort: real duplicates are byte-identical (same timestamp), so survivor choice is stable.
  return dedupeMessages(
    [...messages].sort((a, b) => {
      if (b.timestamp_ms !== a.timestamp_ms) return b.timestamp_ms - a.timestamp_ms;
      if (b.partition !== a.partition) return b.partition - a.partition;
      return b.offset - a.offset;
    }),
  );
}
