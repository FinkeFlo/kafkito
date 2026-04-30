import { describe, expect, it } from "vitest";
import { formatBytes, formatCount, formatDuration, formatRate } from "./format";

describe("formatDuration", () => {
  it("returns — for nullish / non-finite", () => {
    expect(formatDuration(null)).toBe("—");
    expect(formatDuration(undefined)).toBe("—");
    expect(formatDuration(NaN)).toBe("—");
  });

  it("returns ∞ for negative values (Kafka 'infinite retention' convention)", () => {
    expect(formatDuration(-1)).toBe("∞");
    expect(formatDuration(-1000)).toBe("∞");
  });

  it("renders sub-second durations in ms", () => {
    expect(formatDuration(0)).toBe("0");
    expect(formatDuration(1)).toBe("1 ms");
    expect(formatDuration(250)).toBe("250 ms");
    expect(formatDuration(999)).toBe("999 ms");
  });

  it("scales through s, m, h, d, mo, y", () => {
    expect(formatDuration(1_500)).toBe("2 s");
    expect(formatDuration(60_000)).toBe("1 m");
    expect(formatDuration(3_600_000)).toBe("1.0 h"); // 1 hour
    expect(formatDuration(86_400_000)).toBe("1.0 d"); // 1 day
    expect(formatDuration(7 * 86_400_000)).toBe("7.0 d");
    expect(formatDuration(60 * 86_400_000)).toBe("2 mo");
    expect(formatDuration(365 * 86_400_000)).toBe("1.0 y");
  });
});

describe("formatRate", () => {
  it("handles nullish / negative / non-finite", () => {
    expect(formatRate(null)).toBe("—");
    expect(formatRate(undefined)).toBe("—");
    expect(formatRate(-1)).toBe("—");
    expect(formatRate(NaN)).toBe("—");
  });

  it("shows 0/s for tiny values", () => {
    expect(formatRate(0)).toBe("0/s");
    expect(formatRate(0.01)).toBe("0/s");
  });

  it("picks the right unit and precision", () => {
    expect(formatRate(0.5)).toBe("0.50/s");
    expect(formatRate(3.4)).toBe("3.4/s");
    expect(formatRate(42)).toBe("42/s");
    expect(formatRate(999)).toBe("999/s");
    expect(formatRate(1_500)).toBe("1.5k/s");
    expect(formatRate(2_500_000)).toBe("2.50M/s");
  });
});

describe("formatBytes", () => {
  it("handles nullish", () => {
    expect(formatBytes(null)).toBe("—");
    expect(formatBytes(undefined)).toBe("—");
  });

  it("scales IEC units correctly", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.00 KiB");
    expect(formatBytes(1_048_576)).toBe("1.00 MiB");
    expect(formatBytes(1_073_741_824)).toBe("1.00 GiB");
  });
});

describe("formatCount", () => {
  it("handles nullish", () => {
    expect(formatCount(null)).toBe("—");
    expect(formatCount(undefined)).toBe("—");
  });

  it("scales k/M/B at the right thresholds", () => {
    expect(formatCount(42)).toBe("42");
    expect(formatCount(1_500)).toBe("1.5k");
    expect(formatCount(2_500_000)).toBe("2.50M");
    expect(formatCount(3_200_000_000)).toBe("3.20B");
  });
});
