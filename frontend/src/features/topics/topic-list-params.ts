import {
  PARTITION_BUCKETS,
  RETENTION_BUCKETS,
  type PartitionBucket,
  type RetentionBucket,
} from "./topic-filters";

export const TOPIC_SORT_KEYS = [
  "name",
  "partitions",
  "rf",
  "messages",
  "size",
  "rate",
  "lag",
  "retention",
] as const;
export type TopicSortKey = (typeof TOPIC_SORT_KEYS)[number];

/** URL state of the topic list. Defaults are left out so the URL stays short. */
export interface TopicListSearch {
  q?: string;
  internal?: boolean;
  partitions?: PartitionBucket;
  retention?: RetentionBucket;
  sort?: TopicSortKey;
  dir?: "asc" | "desc";
}

function oneOf<T extends string>(values: readonly T[], v: unknown): T | undefined {
  return typeof v === "string" && (values as readonly string[]).includes(v) ? (v as T) : undefined;
}

export function parseTopicListSearch(s: Record<string, unknown>): TopicListSearch {
  const out: TopicListSearch = {};
  // The router JSON-parses search values, so `?q=42` arrives as a number.
  const q = typeof s.q === "string" || typeof s.q === "number" ? String(s.q) : "";
  if (q) out.q = q;
  if (s.internal === true || s.internal === "true") out.internal = true;
  const partitions = oneOf(PARTITION_BUCKETS, s.partitions);
  if (partitions) out.partitions = partitions;
  const retention = oneOf(RETENTION_BUCKETS, s.retention);
  if (retention) out.retention = retention;
  const sort = oneOf(TOPIC_SORT_KEYS, s.sort);
  if (sort) {
    out.sort = sort;
    out.dir = oneOf(["asc", "desc"] as const, s.dir) ?? "asc";
  }
  return out;
}
