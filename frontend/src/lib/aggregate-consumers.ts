import type { GroupInfo, GroupDetail } from "./api";

export interface TopicConsumerSummary {
  group: string;
  state: string;
  members: number;
  lag: number;
}

/**
 * Pure function: given a list of groups (with detail data containing offsets),
 * return a map of `topic -> groups consuming it`.
 *
 * The shape is intentionally minimal — extend by adding fields here, never
 * inline derive at the component level (per usability.md §6).
 *
 * Usage:
 *   const map = useMemo(() => aggregateConsumers(groupDetails), [groupDetails]);
 *   const consumers = map.get(topic) ?? [];
 */
export function aggregateConsumers(
  groups: ReadonlyArray<GroupDetail>,
): Map<string, TopicConsumerSummary[]> {
  const out = new Map<string, TopicConsumerSummary[]>();
  for (const g of groups) {
    const perTopic = new Map<string, number>();
    for (const off of g.offsets ?? []) {
      perTopic.set(off.topic, (perTopic.get(off.topic) ?? 0) + (off.lag ?? 0));
    }
    for (const [topic, lag] of perTopic) {
      const list = out.get(topic) ?? [];
      list.push({
        group: g.group_id,
        state: g.state,
        members: Array.isArray(g.members) ? g.members.length : 0,
        lag,
      });
      out.set(topic, list);
    }
  }
  // Stable order: highest lag first, then group id.
  for (const [topic, list] of out) {
    list.sort((a, b) => b.lag - a.lag || a.group.localeCompare(b.group));
    out.set(topic, list);
  }
  return out;
}

/**
 * Lightweight variant when only `GroupInfo` (without offsets) is available.
 * Cannot resolve per-topic lag; returns empty map.
 * Use only as placeholder until a richer endpoint is wired (see plan §2).
 */
export function aggregateConsumersFromInfo(
  _groups: ReadonlyArray<GroupInfo>,
): Map<string, TopicConsumerSummary[]> {
  return new Map();
}
