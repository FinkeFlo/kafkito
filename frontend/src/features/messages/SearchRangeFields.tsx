import type { TimeRangeState } from "./use-time-range-state";

const PRESETS = ["5m", "15m", "1h", "6h", "24h", "7d", "30d", "today", "yesterday"] as const;

/** Time-range row of the search panel: off, a preset, or custom bounds. */
export function SearchRangeFields({ range }: { range: TimeRangeState }) {
  const {
    mode: rangeMode,
    setMode: setRangeMode,
    preset,
    setPreset,
    customFrom,
    setCustomFrom,
    customTo,
    setCustomTo,
  } = range;

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <label className="font-medium" htmlFor="search-range">
        Range
      </label>
      <select
        id="search-range"
        value={rangeMode}
        onChange={(e) => setRangeMode(e.target.value as typeof rangeMode)}
        className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
      >
        <option value="off">off</option>
        <option value="preset">Preset</option>
        <option value="custom">Custom</option>
      </select>
      {rangeMode === "preset" && (
        <div className="flex flex-wrap items-center gap-1">
          {PRESETS.map((p) => (
            <button
              type="button"
              key={p}
              onClick={() => setPreset(p)}
              className={`rounded border px-2 py-1 ${
                preset === p
                  ? "border-accent bg-accent-subtle text-accent"
                  : "border-[var(--color-border)] bg-[var(--color-surface-raised)] hover:border-[var(--color-border-strong)]"
              }`}
            >
              {p === "today" ? "Today" : p === "yesterday" ? "Yesterday" : `Last ${p}`}
            </button>
          ))}
        </div>
      )}
      {rangeMode === "custom" && (
        <>
          <input
            type="datetime-local"
            aria-label="Search range from"
            value={customFrom}
            onChange={(e) => setCustomFrom(e.target.value)}
            className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
          />
          <span>→</span>
          <input
            type="datetime-local"
            aria-label="Search range to"
            value={customTo}
            onChange={(e) => setCustomTo(e.target.value)}
            className="rounded border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1"
          />
        </>
      )}
    </div>
  );
}
