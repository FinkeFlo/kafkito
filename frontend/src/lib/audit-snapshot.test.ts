import { describe, expect, it } from "vitest";
import {
  type AuditSnapshotInput,
  MAX_BODY_LINES,
  snapshotFilename,
  snapshotModel,
  WRAP_COLUMNS,
  wrapText,
} from "./audit-snapshot";

const base: AuditSnapshotInput = {
  capturedAt: new Date(Date.UTC(2026, 9, 19, 13, 12, 4, 123)),
  zone: "utc",
  user: "fkube",
  email: "fkube@example.com",
  cluster: "prod-eu",
  isProd: true,
  topic: "orders.v1",
  partition: "0",
  offset: "6,280,175",
  timestampMs: Date.UTC(2026, 9, 19, 12, 43, 33, 579),
  key: "ORD-4711",
  fingerprint: "kfp1:x:abc",
  notes: [],
  valueLabel: "value · json",
  value: '{\n  "id": 1\n}',
};

const field = (m: ReturnType<typeof snapshotModel>, label: string) =>
  m.fields.find(([l]) => l === label)?.[1];

describe("snapshotModel", () => {
  it("carries capture time, user, cluster, topic, offset, record time and key", () => {
    const m = snapshotModel(base);
    expect(field(m, "Captured")).toBe("2026-10-19T13:12:04.123Z");
    expect(field(m, "Captured by")).toBe("fkube <fkube@example.com>");
    expect(field(m, "Cluster")).toBe("prod-eu  [PROD]");
    expect(field(m, "Topic")).toBe("orders.v1");
    expect(field(m, "Partition")).toBe("0");
    expect(field(m, "Offset")).toBe("6,280,175");
    expect(field(m, "Record time")).toBe("2026-10-19T12:43:33.579Z");
    expect(field(m, "Key")).toBe("ORD-4711");
    expect(field(m, "Fingerprint")).toBe("kfp1:x:abc");
    expect(m.body).toEqual(["{", '  "id": 1', "}"]);
    expect(m.omittedLines).toBe(0);
  });

  it("shows a dash without a user, no PROD tag on other clusters, and the notes", () => {
    const m = snapshotModel({
      ...base,
      user: null,
      email: null,
      isProd: false,
      key: null,
      fingerprint: null,
      notes: ["Value modified by a data_masking rule."],
    });
    expect(field(m, "Captured by")).toBe("—");
    expect(field(m, "Cluster")).toBe("prod-eu");
    expect(field(m, "Key")).toBe("(no key)");
    expect(field(m, "Fingerprint")).toBe("—");
    expect(field(m, "Note")).toBe("Value modified by a data_masking rule.");
  });

  it("names the user once when user and email are the same", () => {
    const m = snapshotModel({ ...base, user: "a@b.c", email: "a@b.c" });
    expect(field(m, "Captured by")).toBe("a@b.c");
  });

  it("caps very long values and counts what it left out", () => {
    const value = Array.from({ length: MAX_BODY_LINES + 10 }, (_, i) => `line ${i}`).join("\n");
    const m = snapshotModel({ ...base, value });
    expect(m.body).toHaveLength(MAX_BODY_LINES);
    expect(m.omittedLines).toBe(10);
  });
});

describe("wrapText", () => {
  it("wraps long lines and keeps blank ones", () => {
    const long = "x".repeat(WRAP_COLUMNS + 5);
    expect(wrapText(`a\n\n${long}`, WRAP_COLUMNS)).toEqual([
      "a",
      "",
      "x".repeat(WRAP_COLUMNS),
      "xxxxx",
    ]);
  });
});

describe("snapshotFilename", () => {
  it("is safe for file systems and names the record", () => {
    const at = new Date(2026, 9, 19, 15, 12, 4);
    expect(snapshotFilename("prod eu/1", "orders.v1", 0, 42, at)).toBe(
      "kafkito-audit-prod_eu_1-orders.v1-p0-o42-20261019T151204.png",
    );
  });
});
