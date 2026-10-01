import { describe, expect, it } from "vitest";
import type { TopicInfo } from "@/lib/api";
import { filterTopics, hasKnownRetention, summarizeTopics } from "./topic-filters";

const DAY = 86_400_000;

function topic(name: string, extra: Partial<TopicInfo> = {}): TopicInfo {
  return { name, partitions: 3, replication_factor: 3, is_internal: false, ...extra };
}

const TOPICS: TopicInfo[] = [
  topic("orders", { partitions: 12, retention_ms: 7 * DAY }),
  topic("audit", { partitions: 1, retention_ms: -1 }),
  topic("short", { partitions: 6, retention_ms: 6 * 3_600_000 }),
  topic("long", { partitions: 2, retention_ms: 30 * DAY }),
  topic("unknown", { partitions: 10 }),
  topic("__consumer_offsets", { partitions: 50, is_internal: true, retention_ms: 7 * DAY }),
];

const names = (ts: TopicInfo[]) => ts.map((t) => t.name);

describe("filterTopics", () => {
  it("hides internal topics by default and sorts by name", () => {
    expect(names(filterTopics(TOPICS, {}))).toEqual([
      "audit",
      "long",
      "orders",
      "short",
      "unknown",
    ]);
  });

  it("includes internal topics when asked", () => {
    expect(names(filterTopics(TOPICS, { showInternal: true }))).toContain("__consumer_offsets");
  });

  it("buckets partitions as 1, 2-10 and more than 10", () => {
    expect(names(filterTopics(TOPICS, { partitions: "1" }))).toEqual(["audit"]);
    expect(names(filterTopics(TOPICS, { partitions: "2-10" }))).toEqual([
      "long",
      "short",
      "unknown",
    ]);
    expect(names(filterTopics(TOPICS, { partitions: "gt10" }))).toEqual(["orders"]);
  });

  it("buckets finite retention by upper bound and keeps infinite separate", () => {
    expect(names(filterTopics(TOPICS, { retention: "le1d" }))).toEqual(["short"]);
    expect(names(filterTopics(TOPICS, { retention: "le7d" }))).toEqual(["orders", "short"]);
    expect(names(filterTopics(TOPICS, { retention: "gt7d" }))).toEqual(["long"]);
    expect(names(filterTopics(TOPICS, { retention: "infinite" }))).toEqual(["audit"]);
  });

  it("matches topics whose retention was not reported with 'unknown'", () => {
    expect(names(filterTopics(TOPICS, { retention: "unknown" }))).toEqual(["unknown"]);
  });

  it("combines partition and retention filters", () => {
    expect(names(filterTopics(TOPICS, { partitions: "2-10", retention: "le7d" }))).toEqual([
      "short",
    ]);
  });
});

describe("hasKnownRetention", () => {
  it("is false when no topic reports retention, e.g. without DESCRIBE_CONFIGS", () => {
    expect(hasKnownRetention([topic("a"), topic("b")])).toBe(false);
    expect(hasKnownRetention(TOPICS)).toBe(true);
  });
});

describe("summarizeTopics", () => {
  it("sums partitions, size and rate and names the largest topic", () => {
    const s = summarizeTopics([
      topic("a", { partitions: 4, size_bytes: 100, rate_per_sec: 12.5 }),
      topic("b", { partitions: 2, size_bytes: 900, rate_per_sec: 0 }),
      topic("c", { partitions: 6, rate_per_sec: 0.05 }),
    ]);
    expect(s).toEqual({
      topics: 3,
      partitions: 12,
      sizeBytes: 1000,
      largest: "b",
      ratePerSec: 12.55,
      idle: 2,
    });
  });

  it("reports unknown size and rate as null instead of zero", () => {
    const s = summarizeTopics([topic("a"), topic("b")]);
    expect(s.sizeBytes).toBeNull();
    expect(s.largest).toBeNull();
    expect(s.ratePerSec).toBeNull();
    expect(s.idle).toBe(0);
  });

  it("handles an empty list", () => {
    expect(summarizeTopics([])).toEqual({
      topics: 0,
      partitions: 0,
      sizeBytes: null,
      largest: null,
      ratePerSec: null,
      idle: 0,
    });
  });
});
