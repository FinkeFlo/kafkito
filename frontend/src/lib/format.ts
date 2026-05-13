/**
 * Project-wide formatting helpers. Single source of truth — never re-implement
 * locally. ISO 8601 timestamps, IEC bytes, locale-aware numbers via Intl.
 *
 * Number-formatting functions accept an optional `locale` argument (BCP-47,
 * e.g. "de-DE"). Omit it to keep the historical en-US output. React UI should
 * pull the active locale through `useFormatters()` so the user-menu toggle
 * (Auto · DE · EN) is honored everywhere.
 *
 * See: .github/instructions/frontend-styleguide.instructions.md §4.
 */

const DEFAULT_LOCALE = "en-US";

const groupedCache = new Map<string, Intl.NumberFormat>();
const compactCache = new Map<string, Intl.NumberFormat>();

function grouped(locale: string): Intl.NumberFormat {
  let nf = groupedCache.get(locale);
  if (!nf) {
    nf = new Intl.NumberFormat(locale);
    groupedCache.set(locale, nf);
  }
  return nf;
}

function compact(locale: string): Intl.NumberFormat {
  let nf = compactCache.get(locale);
  if (!nf) {
    nf = new Intl.NumberFormat(locale, {
      notation: "compact",
      compactDisplay: "short",
      maximumFractionDigits: 2,
      minimumFractionDigits: 0,
    });
    compactCache.set(locale, nf);
  }
  return nf;
}

function fixed(locale: string, digits: number): Intl.NumberFormat {
  return new Intl.NumberFormat(locale, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
}

/** Format an integer count with locale-appropriate thousands separators. */
export function formatNumber(
  n: number | bigint,
  locale: string = DEFAULT_LOCALE,
): string {
  if (typeof n === "bigint") return grouped(locale).format(n);
  if (!Number.isFinite(n)) return "—";
  return grouped(locale).format(n);
}

/**
 * Format a count with locale-aware compact notation for dense data-grid cells.
 * In en-US: `formatCount(8_420_000)` → "8.42M".
 * In de-DE: `formatCount(8_420_000)` → "8,42 Mio.".
 * Falls back to a grouped decimal for values below 1000.
 */
export function formatCount(
  n: number | bigint | null | undefined,
  locale: string = DEFAULT_LOCALE,
): string {
  if (n === null || n === undefined) return "—";
  const num = typeof n === "bigint" ? Number(n) : n;
  if (!Number.isFinite(num)) return "—";
  if (Math.abs(num) < 1_000) return grouped(locale).format(num);
  return compact(locale).format(num);
}

/** Render a consumer-group lag value as a plain string. `null`/`undefined`/NaN → "—". */
export function formatLag(
  lag: number | bigint | null | undefined,
  locale: string = DEFAULT_LOCALE,
): string {
  if (lag === null || lag === undefined) return "—";
  const n = typeof lag === "bigint" ? Number(lag) : lag;
  if (!Number.isFinite(n)) return "—";
  return grouped(locale).format(n);
}

/** UTC-only timestamp (alias for `formatTimestamp(..., "utc")`). */
export function formatTs(input: Date | number | string | null | undefined): string {
  return formatTimestamp(input, "utc");
}

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/**
 * Format a byte count using IEC binary units.
 * Always returns 1–2 fractional digits for values ≥ 1 KiB, 0 for raw bytes.
 * The numeric portion uses locale-aware decimal/thousands separators; the
 * IEC unit suffix (KiB, MiB, …) is technical and stays untranslated.
 */
export function formatBytes(
  bytes: number | bigint | null | undefined,
  locale: string = DEFAULT_LOCALE,
): string {
  if (bytes === null || bytes === undefined) return "—";
  const n = typeof bytes === "bigint" ? Number(bytes) : bytes;
  if (!Number.isFinite(n)) return "—";
  if (n < 1024) return `${grouped(locale).format(Math.trunc(n))} B`;

  let value = n;
  let unitIdx = 0;
  while (value >= 1024 && unitIdx < BYTE_UNITS.length - 1) {
    value /= 1024;
    unitIdx += 1;
  }
  const digits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
  return `${fixed(locale, digits).format(value)} ${BYTE_UNITS[unitIdx]}`;
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
 * The numeric portion uses locale-aware decimal separators; unit suffixes
 * (`ms`, `s`, `m`, `h`, `d`, `mo`, `y`) are kept English/short and stable.
 */
export function formatDuration(
  ms: number | bigint | null | undefined,
  locale: string = DEFAULT_LOCALE,
): string {
  if (ms === null || ms === undefined) return "—";
  const n = typeof ms === "bigint" ? Number(ms) : ms;
  if (!Number.isFinite(n)) return "—";
  if (n < 0) return "∞";
  if (n === 0) return "0";
  const s = n / 1000;
  if (s < 1) return `${grouped(locale).format(Math.round(n))} ms`;
  const m = s / 60;
  if (m < 1) return `${grouped(locale).format(Math.round(s))} s`;
  const h = m / 60;
  if (h < 1) return `${grouped(locale).format(Math.round(m))} m`;
  const d = h / 24;
  if (d < 1)
    return `${h >= 10 ? grouped(locale).format(Math.round(h)) : fixed(locale, 1).format(h)} h`;
  if (d < 30)
    return `${d >= 10 ? grouped(locale).format(Math.round(d)) : fixed(locale, 1).format(d)} d`;
  const y = d / 365;
  if (y < 1) return `${grouped(locale).format(Math.round(d / 30))} mo`;
  return `${y >= 10 ? grouped(locale).format(Math.round(y)) : fixed(locale, 1).format(y)} y`;
}

/**
 * Format a rate (messages per second) compactly: "1.2k/s", "3 msg/s",
 * "42.0M/s". `null`/`undefined`/negative → "—".
 * The numeric portion uses locale-aware decimal separators; the `/s` and
 * `k`/`M` suffixes are kept short and stable across locales.
 */
export function formatRate(
  perSec: number | null | undefined,
  locale: string = DEFAULT_LOCALE,
): string {
  if (perSec === null || perSec === undefined) return "—";
  if (!Number.isFinite(perSec) || perSec < 0) return "—";
  if (perSec < 0.1) return "0/s";
  if (perSec < 1) return `${fixed(locale, 2).format(perSec)}/s`;
  if (perSec < 10) return `${fixed(locale, 1).format(perSec)}/s`;
  if (perSec < 1_000) return `${grouped(locale).format(Math.round(perSec))}/s`;
  if (perSec < 1_000_000) return `${fixed(locale, 1).format(perSec / 1_000)}k/s`;
  return `${fixed(locale, 2).format(perSec / 1_000_000)}M/s`;
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
