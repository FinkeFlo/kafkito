import { useMemo } from "react";
import {
  formatBytes,
  formatCount,
  formatDecimal,
  formatDuration,
  formatLag,
  formatNumber,
  formatRate,
} from "./format";
import { useNumberFormat } from "./use-number-format";

export type Formatters = {
  number: (n: number | bigint) => string;
  count: (n: number | bigint | null | undefined) => string;
  lag: (n: number | bigint | null | undefined) => string;
  bytes: (n: number | bigint | null | undefined) => string;
  rate: (n: number | null | undefined) => string;
  duration: (n: number | bigint | null | undefined) => string;
  decimal: (n: number | bigint | null | undefined, digits: number) => string;
};

/**
 * Returns a stable object of formatter functions bound to the active locale.
 * The object reference changes whenever the user switches locale in the user
 * menu (or another tab does), so callers may pass these into memoized props
 * without breaking referential equality on benign renders.
 */
export function useFormatters(): Formatters {
  const { effectiveLocale } = useNumberFormat();

  return useMemo<Formatters>(
    () => ({
      number: (n) => formatNumber(n, effectiveLocale),
      count: (n) => formatCount(n, effectiveLocale),
      lag: (n) => formatLag(n, effectiveLocale),
      bytes: (n) => formatBytes(n, effectiveLocale),
      rate: (n) => formatRate(n, effectiveLocale),
      duration: (n) => formatDuration(n, effectiveLocale),
      decimal: (n, digits) => formatDecimal(n, digits, effectiveLocale),
    }),
    [effectiveLocale],
  );
}
