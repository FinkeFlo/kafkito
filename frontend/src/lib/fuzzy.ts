// Fuzzy search helper around Fuse.js. Centralizes the defaults we want for
// Kafka-style identifiers (dot/dash/underscore, usually short) and exposes a
// small hook that returns filtered+sorted results plus highlight ranges for
// each match.
//
// We enable Fuse's extended search so that typing multiple space-separated
// tokens acts as an AND — matching the "sales qas" → "sales.qas.v1" case.

import Fuse, { type FuseResultMatch, type IFuseOptions } from "fuse.js";
import { useMemo } from "react";

export type HighlightRange = readonly [number, number];

export interface FuzzyResult<T> {
  /** Filtered + ranked items when a query is present; original order otherwise. */
  results: T[];
  /** Ranges into the given key's string for a result (empty when no query). */
  rangesFor(item: T, key: string): HighlightRange[];
}

export interface UseFuzzyOptions<T> {
  /** Keys to search over. Paths allowed (dot notation). */
  keys: readonly (keyof T & string)[] | readonly string[];
  /** User query — already trimmed is fine, we trim defensively. */
  query: string;
  /** Fuse threshold (0 = exact, 1 = match anything). Default 0.35. */
  threshold?: number;
  /** Minimum characters a match must have. Default 2. */
  minMatchCharLength?: number;
}

const FUSE_DEFAULTS: Partial<IFuseOptions<unknown>> = {
  ignoreLocation: true,
  includeMatches: true,
  useExtendedSearch: true,
  // Threshold is effectively ignored when every token uses the 'exact-match
  // extended-search operator, but keep it strict as a safety net.
  threshold: 0.0,
  minMatchCharLength: 2,
  shouldSort: true,
};

/**
 * Turns a user query into a Fuse extended-search expression that requires
 * every whitespace-separated token to appear as a literal substring in the
 * record (order-independent). This avoids Fuse's character-subsequence
 * fuzziness, which produced confusing matches for short tokens (e.g. "abc"
 * matching "_bc" in "abcdef", or "es" in "prices").
 */
function buildExtendedQuery(raw: string): string {
  const tokens = raw.trim().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return "";
  // `'token` means "include matches that contain this literal substring".
  // Escaping isn't needed: Fuse only reserves the leading operator chars.
  return tokens.map((t) => `'${stripOperators(t)}`).join(" ");
}

/**
 * Removes leading Fuse extended-search operator characters a user might
 * paste in ( ' ^ ! = ), so they become part of the literal search instead
 * of changing matcher semantics.
 */
function stripOperators(t: string): string {
  return t.replace(/^['^!=]+/, "");
}

/**
 * Pure, stateless implementation of the fuzzy search. Exported separately
 * from the hook so it can be unit-tested and reused in non-React contexts.
 */
export function fuzzyFilter<T>(items: T[], opts: UseFuzzyOptions<T>): FuzzyResult<T> {
  const { keys, query, threshold, minMatchCharLength } = opts;
  const q = query.trim();
  if (!q) {
    return { results: items, rangesFor: () => [] };
  }
  const fuse = new Fuse(items, {
    ...(FUSE_DEFAULTS as IFuseOptions<T>),
    keys: keys as string[],
    threshold: threshold ?? FUSE_DEFAULTS.threshold,
    minMatchCharLength: minMatchCharLength ?? FUSE_DEFAULTS.minMatchCharLength,
  });
  const hits = fuse.search(buildExtendedQuery(q));
  const byItem = new Map<T, FuseResultMatch[]>();
  for (const h of hits) if (h.matches) byItem.set(h.item, h.matches as FuseResultMatch[]);
  return {
    results: hits.map((h) => h.item),
    rangesFor(item, key) {
      const matches = byItem.get(item);
      if (!matches) return [];
      const m = matches.find((mm) => mm.key === key);
      return (m?.indices ?? []) as readonly HighlightRange[] as HighlightRange[];
    },
  };
}

export function useFuzzy<T>(items: T[], opts: UseFuzzyOptions<T>): FuzzyResult<T> {
  const { keys, query, threshold, minMatchCharLength } = opts;
  return useMemo(
    () => fuzzyFilter(items, { keys, query, threshold, minMatchCharLength }),
    [items, keys, query, threshold, minMatchCharLength],
  );
}
