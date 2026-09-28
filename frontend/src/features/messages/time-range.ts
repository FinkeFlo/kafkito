export type TimeRangeMode = "off" | "preset" | "custom";

export type TimeRange = { from_ts_ms: number | undefined; to_ts_ms: number | undefined };

const PRESET_DURATIONS_MS: Record<string, number> = {
  "5m": 5 * 60_000,
  "15m": 15 * 60_000,
  "1h": 60 * 60_000,
  "6h": 6 * 60 * 60_000,
  "24h": 24 * 60 * 60_000,
  "7d": 7 * 24 * 60 * 60_000,
  "30d": 30 * 24 * 60 * 60_000,
};

/** Resolves a range selection to timestamps; presets are relative to now. */
export function computeTimeRange(
  mode: TimeRangeMode,
  preset: string,
  customFrom: string,
  customTo: string,
): TimeRange {
  if (mode === "off") return { from_ts_ms: undefined, to_ts_ms: undefined };
  if (mode === "preset") {
    const now = Date.now();
    if (preset === "today") {
      const s = new Date();
      s.setHours(0, 0, 0, 0);
      return { from_ts_ms: s.getTime(), to_ts_ms: now };
    }
    if (preset === "yesterday") {
      const s = new Date();
      s.setHours(0, 0, 0, 0);
      s.setDate(s.getDate() - 1);
      const e = new Date(s);
      e.setHours(23, 59, 59, 999);
      return { from_ts_ms: s.getTime(), to_ts_ms: e.getTime() };
    }
    return {
      from_ts_ms: now - (PRESET_DURATIONS_MS[preset] ?? 24 * 60 * 60_000),
      to_ts_ms: now,
    };
  }
  return {
    from_ts_ms: customFrom ? new Date(customFrom).getTime() : undefined,
    to_ts_ms: customTo ? new Date(customTo).getTime() : undefined,
  };
}
