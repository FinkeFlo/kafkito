import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { computeTimeRange } from "./time-range";

const NOW = new Date(2026, 2, 15, 13, 45, 30, 250).getTime();

describe("computeTimeRange", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns no bounds when off", () => {
    expect(computeTimeRange("off", "1h", "2026-01-01T00:00", "")).toEqual({
      from_ts_ms: undefined,
      to_ts_ms: undefined,
    });
  });

  it.each([
    ["5m", 5 * 60_000],
    ["15m", 15 * 60_000],
    ["1h", 60 * 60_000],
    ["6h", 6 * 60 * 60_000],
    ["24h", 24 * 60 * 60_000],
    ["7d", 7 * 24 * 60 * 60_000],
    ["30d", 30 * 24 * 60 * 60_000],
  ])("preset %s ends now and spans its duration", (preset, span) => {
    expect(computeTimeRange("preset", preset, "", "")).toEqual({
      from_ts_ms: NOW - span,
      to_ts_ms: NOW,
    });
  });

  it("falls back to 24h for an unknown preset", () => {
    expect(computeTimeRange("preset", "bogus", "", "")).toEqual({
      from_ts_ms: NOW - 24 * 60 * 60_000,
      to_ts_ms: NOW,
    });
  });

  it("today runs from local midnight to now", () => {
    expect(computeTimeRange("preset", "today", "", "")).toEqual({
      from_ts_ms: new Date(2026, 2, 15, 0, 0, 0, 0).getTime(),
      to_ts_ms: NOW,
    });
  });

  it("yesterday covers the whole previous local day", () => {
    expect(computeTimeRange("preset", "yesterday", "", "")).toEqual({
      from_ts_ms: new Date(2026, 2, 14, 0, 0, 0, 0).getTime(),
      to_ts_ms: new Date(2026, 2, 14, 23, 59, 59, 999).getTime(),
    });
  });

  it("custom parses each bound independently", () => {
    expect(computeTimeRange("custom", "24h", "2026-03-01T10:00", "")).toEqual({
      from_ts_ms: new Date("2026-03-01T10:00").getTime(),
      to_ts_ms: undefined,
    });
    expect(computeTimeRange("custom", "24h", "", "2026-03-02T11:30")).toEqual({
      from_ts_ms: undefined,
      to_ts_ms: new Date("2026-03-02T11:30").getTime(),
    });
  });
});
