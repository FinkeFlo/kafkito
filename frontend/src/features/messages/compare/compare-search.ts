import { type CompareOptions, DEFAULT_COMPARE_OPTIONS } from "@/lib/message-compare";

export type CompareView = "exact" | "semantic";

/**
 * URL state of the compare page. `a` and `b` are message references
 * (`cluster/topic/partition/offset`); `b` may also be a `kfp1:` fingerprint.
 * Options are only present when they differ from the defaults.
 */
export interface CompareSearch {
  a?: string;
  b?: string;
  view?: "exact";
  anyArrayOrder?: true;
  anyElementOrder?: true;
  /** Compare the X-Kafkito-* provenance headers too. */
  kafkitoHeaders?: true;
  /** Hide unchanged lines and rows. */
  onlyDiff?: true;
}

export function parseCompareSearch(s: Record<string, unknown>): CompareSearch {
  const out: CompareSearch = {};
  if (typeof s.a === "string" && s.a.trim()) out.a = s.a.trim();
  if (typeof s.b === "string" && s.b.trim()) out.b = s.b.trim();
  if (s.view === "exact") out.view = "exact";
  if (s.anyArrayOrder === true) out.anyArrayOrder = true;
  if (s.anyElementOrder === true) out.anyElementOrder = true;
  if (s.kafkitoHeaders === true) out.kafkitoHeaders = true;
  if (s.onlyDiff === true) out.onlyDiff = true;
  return out;
}

export function compareView(s: CompareSearch): CompareView {
  return s.view ?? "semantic";
}

export function compareOptions(s: CompareSearch): CompareOptions {
  return {
    ...DEFAULT_COMPARE_OPTIONS,
    ignoreArrayOrder: s.anyArrayOrder === true,
    ignoreChildOrder: s.anyElementOrder === true,
    ignoreKafkitoHeaders: s.kafkitoHeaders !== true,
  };
}

/** The search params that select `opts` and `view`; defaults are left out. */
export function optionsSearch(
  opts: CompareOptions,
  view: CompareView,
): Pick<CompareSearch, "view" | "anyArrayOrder" | "anyElementOrder" | "kafkitoHeaders"> {
  return {
    view: view === "exact" ? "exact" : undefined,
    anyArrayOrder: opts.ignoreArrayOrder ? true : undefined,
    anyElementOrder: opts.ignoreChildOrder ? true : undefined,
    kafkitoHeaders: opts.ignoreKafkitoHeaders ? undefined : true,
  };
}
