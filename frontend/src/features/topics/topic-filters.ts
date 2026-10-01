import type { TopicInfo } from "@/lib/api";

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

/** Below this many messages per second a topic counts as idle (formatRate prints "0/s"). */
export const IDLE_RATE = 0.1;

export const PARTITION_BUCKETS = ["1", "2-10", "gt10"] as const;
export type PartitionBucket = (typeof PARTITION_BUCKETS)[number];

export const RETENTION_BUCKETS = ["le1d", "le7d", "gt7d", "infinite", "unknown"] as const;
export type RetentionBucket = (typeof RETENTION_BUCKETS)[number];

export interface TopicFilters {
  showInternal?: boolean;
  partitions?: PartitionBucket;
  retention?: RetentionBucket;
}

function matchesPartitions(t: TopicInfo, bucket: PartitionBucket | undefined): boolean {
  switch (bucket) {
    case undefined:
      return true;
    case "1":
      return t.partitions === 1;
    case "2-10":
      return t.partitions >= 2 && t.partitions <= 10;
    case "gt10":
      return t.partitions > 10;
  }
}

// Kafka reports infinite retention as -1. A missing value means the broker
// did not tell us (typically no DESCRIBE_CONFIGS), which is its own bucket.
function matchesRetention(t: TopicInfo, bucket: RetentionBucket | undefined): boolean {
  const ms = t.retention_ms;
  if (bucket === undefined) return true;
  if (bucket === "unknown") return ms === undefined || ms === null;
  if (ms === undefined || ms === null) return false;
  if (bucket === "infinite") return ms < 0;
  if (ms < 0) return false;
  switch (bucket) {
    case "le1d":
      return ms <= DAY;
    case "le7d":
      return ms <= 7 * DAY;
    case "gt7d":
      return ms > 7 * DAY;
  }
}

/** Applies the toolbar filters (everything except the name search) and sorts by name. */
export function filterTopics(topics: TopicInfo[], filters: TopicFilters): TopicInfo[] {
  return topics
    .filter(
      (t) =>
        (filters.showInternal || !t.is_internal) &&
        matchesPartitions(t, filters.partitions) &&
        matchesRetention(t, filters.retention),
    )
    .sort((a, b) => a.name.localeCompare(b.name));
}

/** False when no topic reports retention, so filtering by it cannot work. */
export function hasKnownRetention(topics: TopicInfo[]): boolean {
  return topics.some((t) => t.retention_ms !== undefined && t.retention_ms !== null);
}

export interface TopicSummary {
  topics: number;
  partitions: number;
  /** Null when no topic reports its size. */
  sizeBytes: number | null;
  largest: string | null;
  /** Null when no topic reports a rate. */
  ratePerSec: number | null;
  idle: number;
}

/** Aggregates for the KPI strip. Unknown metrics stay null, never zero. */
export function summarizeTopics(topics: TopicInfo[]): TopicSummary {
  let partitions = 0;
  let sizeBytes: number | null = null;
  let largest: TopicInfo | null = null;
  let ratePerSec: number | null = null;
  let idle = 0;
  for (const t of topics) {
    partitions += t.partitions;
    if (t.size_bytes !== undefined && t.size_bytes !== null) {
      sizeBytes = (sizeBytes ?? 0) + t.size_bytes;
      if (!largest || t.size_bytes > (largest.size_bytes ?? 0)) largest = t;
    }
    if (t.rate_per_sec !== undefined && t.rate_per_sec !== null) {
      ratePerSec = (ratePerSec ?? 0) + t.rate_per_sec;
      if (t.rate_per_sec < IDLE_RATE) idle++;
    }
  }
  return {
    topics: topics.length,
    partitions,
    sizeBytes,
    largest: largest?.name ?? null,
    ratePerSec,
    idle,
  };
}
