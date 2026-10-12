import type { Message } from "@/lib/api";

export interface HeaderKeyStat {
  key: string;
  /** Number of the given messages that carry the header. */
  count: number;
}

/**
 * The header keys of the given messages with the number of messages that
 * carry each, most frequent first, then by key.
 */
export function headerKeyStats(messages: Pick<Message, "headers">[]): HeaderKeyStat[] {
  const counts = new Map<string, number>();
  for (const m of messages) {
    for (const key of Object.keys(m.headers ?? {})) {
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
  }
  return [...counts]
    .map(([key, count]) => ({ key, count }))
    .sort((a, b) => b.count - a.count || a.key.localeCompare(b.key));
}
