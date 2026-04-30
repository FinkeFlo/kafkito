/**
 * Project-wide formatting helpers. Single source of truth — never re-implement
 * locally. ISO 8601 timestamps, IEC bytes, en-US numbers (i18n-ready later).
 *
 * See: .github/instructions/frontend-styleguide.instructions.md §4.
 */

const numberFormatter = new Intl.NumberFormat("en-US");

/** Format an integer count with thin thousands separators. */
export function formatNumber(n: number | bigint): string {
  if (typeof n === "bigint") return numberFormatter.format(n);
  if (!Number.isFinite(n)) return "—";
  return numberFormatter.format(n);
}

/**
 * Format a count with k/M/B compaction for dense data-grid cells.
 * `formatCount(8_420_000)` → "8.42M"; `formatCount(110_000)` → "110.0k".
 * Falls back to a grouped decimal for values below 1000.
 */
export function formatCount(n: number | bigint | null | undefined): string {
  if (n === null || n === undefined) return "—";
  const num = typeof n === "bigint" ? Number(n) : n;
  if (!Number.isFinite(num)) return "—";
  const abs = Math.abs(num);
  if (abs < 1_000) return numberFormatter.format(num);
  if (abs < 1_000_000) return `${(num / 1_000).toFixed(1)}k`;
  if (abs < 1_000_000_000) return `${(num / 1_000_000).toFixed(2)}M`;
  return `${(num / 1_000_000_000).toFixed(2)}B`;
}

/** Render a consumer-group lag value as a plain string. `null`/`undefined`/NaN → "—". */
export function formatLag(lag: number | bigint | null | undefined): string {
  if (lag === null || lag === undefined) return "—";
  const n = typeof lag === "bigint" ? Number(lag) : lag;
  if (!Number.isFinite(n)) return "—";
  return numberFormatter.format(n);
}

/** UTC-only timestamp (alias for `formatTimestamp(..., "utc")`). */
export function formatTs(input: Date | number | string | null | undefined): string {
  return formatTimestamp(input, "utc");
}

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/**
 * Format a byte count using IEC binary units.
 * Always returns 1–2 fractional digits for values ≥ 1 KiB, 0 for raw bytes.
 */
export function formatBytes(bytes: number | bigint | null | undefined): string {
  if (bytes === null || bytes === undefined) return "—";
  const n = typeof bytes === "bigint" ? Number(bytes) : bytes;
  if (!Number.isFinite(n)) return "—";
  if (n < 1024) return `${Math.trunc(n)} B`;

  let value = n;
  let unitIdx = 0;
  while (value >= 1024 && unitIdx < BYTE_UNITS.length - 1) {
    value /= 1024;
    unitIdx += 1;
  }
  const digits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
  return `${value.toFixed(digits)} ${BYTE_UNITS[unitIdx]}`;
}

export type TimestampZone = "utc" | "local";

function pad(n: number, width = 2): string {
  return String(n).padStart(width, "0");
}

/**
 * Format a timestamp as ISO 8601 with millisecond precision.
 * UTC default → "YYYY-MM-DDTHH:mm:ss.SSSZ"
 * Local       → "YYYY-MM-DDTHH:mm:ss.SSS±HH:mm"
 *
 * Accepts: Date | epoch ms (number) | epoch ms or ISO string.
 */
export function formatTimestamp(
  input: Date | number | string | null | undefined,
  zone: TimestampZone = "utc",
): string {
  if (input === null || input === undefined || input === "") return "—";
  const date = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(date.getTime())) return "—";

  if (zone === "utc") {
    return (
      `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}` +
      `T${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}:${pad(date.getUTCSeconds())}` +
      `.${pad(date.getUTCMilliseconds(), 3)}Z`
    );
  }

  const offsetMinTotal = -date.getTimezoneOffset();
  const sign = offsetMinTotal >= 0 ? "+" : "-";
  const offsetAbs = Math.abs(offsetMinTotal);
  const offset = `${sign}${pad(Math.floor(offsetAbs / 60))}:${pad(offsetAbs % 60)}`;

  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
    `T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}` +
    `.${pad(date.getMilliseconds(), 3)}${offset}`
  );
}

const RELATIVE_DIVISIONS: Array<{ amount: number; unit: Intl.RelativeTimeFormatUnit }> = [
  { amount: 60, unit: "second" },
  { amount: 60, unit: "minute" },
  { amount: 24, unit: "hour" },
  { amount: 7, unit: "day" },
  { amount: 4.34524, unit: "week" },
  { amount: 12, unit: "month" },
  { amount: Number.POSITIVE_INFINITY, unit: "year" },
];

const relativeFormatter = new Intl.RelativeTimeFormat("en-US", { numeric: "auto" });

/** Format a timestamp as a relative duration ("3 minutes ago", "in 2 days"). */
export function formatRelative(
  input: Date | number | string | null | undefined,
  now: Date = new Date(),
): string {
  if (input === null || input === undefined || input === "") return "—";
  const date = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(date.getTime())) return "—";

  let duration = (date.getTime() - now.getTime()) / 1000;
  for (const division of RELATIVE_DIVISIONS) {
    if (Math.abs(duration) < division.amount) {
      return relativeFormatter.format(Math.round(duration), division.unit);
    }
    duration /= division.amount;
  }
  return relativeFormatter.format(Math.round(duration), "year");
}

/**
 * Format a duration given in milliseconds as a compact human string.
 * Kafka retention convention: -1 ms means "infinite". `0`/unknown → "—".
 *   7 d, 12 h, 30 m, 45 s, 500 ms
 */
export function formatDuration(ms: number | bigint | null | undefined): string {
  if (ms === null || ms === undefined) return "—";
  const n = typeof ms === "bigint" ? Number(ms) : ms;
  if (!Number.isFinite(n)) return "—";
  if (n < 0) return "∞";
  if (n === 0) return "0";
  const s = n / 1000;
  if (s < 1) return `${Math.round(n)} ms`;
  const m = s / 60;
  if (m < 1) return `${Math.round(s)} s`;
  const h = m / 60;
  if (h < 1) return `${Math.round(m)} m`;
  const d = h / 24;
  if (d < 1) return `${h >= 10 ? Math.round(h) : h.toFixed(1)} h`;
  if (d < 30) return `${d >= 10 ? Math.round(d) : d.toFixed(1)} d`;
  const y = d / 365;
  if (y < 1) return `${Math.round(d / 30)} mo`;
  return `${y >= 10 ? Math.round(y) : y.toFixed(1)} y`;
}

/**
 * Format a rate (messages per second) compactly: "1.2k/s", "3 msg/s",
 * "42.0M/s". `null`/`undefined`/negative → "—".
 */
export function formatRate(perSec: number | null | undefined): string {
  if (perSec === null || perSec === undefined) return "—";
  if (!Number.isFinite(perSec) || perSec < 0) return "—";
  if (perSec < 0.1) return "0/s";
  if (perSec < 1) return `${perSec.toFixed(2)}/s`;
  if (perSec < 10) return `${perSec.toFixed(1)}/s`;
  if (perSec < 1_000) return `${Math.round(perSec)}/s`;
  if (perSec < 1_000_000) return `${(perSec / 1_000).toFixed(1)}k/s`;
  return `${(perSec / 1_000_000).toFixed(2)}M/s`;
}

export type LagVariant = "neutral" | "warning" | "danger";

/**
 * Map a lag value to its semantic variant. Single source of truth for thresholds
 * (see frontend-styleguide §3). Internal — UI must use <LagBadge> instead.
 *
 * @internal
 */
export function lagVariant(lag: number | bigint | null | undefined): LagVariant {
  if (lag === null || lag === undefined) return "neutral";
  const n = typeof lag === "bigint" ? Number(lag) : lag;
  if (!Number.isFinite(n) || n < 1_000) return "neutral";
  if (n < 10_000) return "warning";
  return "danger";
}
