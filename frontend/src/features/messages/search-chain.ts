import { searchMessages, type Message, type SearchRequest, type SearchStats } from "@/lib/api";

/**
 * Why a search chain stopped. "timeout" means a call reported more to search
 * but made no progress (typically its server-side timeout hit before it could
 * read anything new); "Search more" can retry from the same position.
 */
export type SearchStopReason = "budget" | "complete" | "limit" | "stopped" | "timeout";

export type SearchResult = { messages: Message[]; stats: SearchStats; req: SearchRequest };

// Per-call cap used in unlimited mode; the 12s server timeout is the real
// limiter, this just keeps each request bounded.
const PER_CALL_BUDGET = 1_000_000;

type ChainOptions = {
  cluster: string;
  topic: string;
  baseReq: SearchRequest;
  /** Result to continue from ("Search more"); null starts fresh. */
  prior: SearchResult | null;
  /** Records to scan in this run; 0 or less scans until the range is exhausted. */
  budget: number;
  stopOnLimit: boolean;
  limit: number;
  shouldStop: () => boolean;
  /** Called with the cumulative result after every call. */
  onProgress: (result: SearchResult) => void;
};

/**
 * Reports whether a call moved any partition's cursor off the boundary of the
 * range it searched: the end when newest first, the start when oldest first.
 * A call without progress would be repeated with the same cursors forever.
 */
function madeProgress(s: SearchStats): boolean {
  if (s.scanned === 0) return false;
  const range = s.resolved_range;
  const next = s.next_cursors;
  if (!range || !next) return true;
  const newestFirst = s.direction !== "oldest_first";
  return Object.entries(range).some(([p, r]) => {
    const c = next[p];
    return c !== undefined && c !== (newestFirst ? r.end : r.start);
  });
}

/**
 * Chains search calls, feeding each call's next_cursors into the next, until
 * the range is exhausted, the budget or limit is reached, or `shouldStop`
 * turns true. Returns why it stopped; rejects when a call fails.
 */
export async function runSearchChain({
  cluster,
  topic,
  baseReq,
  prior,
  budget,
  stopOnLimit,
  limit,
  shouldStop,
  onProgress,
}: ChainOptions): Promise<SearchStopReason> {
  // Seed the accumulator from the existing result when continuing ("Search
  // more"), otherwise start fresh. The Budget applies per invocation: a fresh
  // search scans up to `budget` records; "Search more" grants another budget.
  let accMessages: Message[] = prior ? [...prior.messages] : [];
  let accScanned = prior ? prior.stats.scanned : 0;
  let accMatched = prior ? prior.stats.matched : 0;
  let cursors: Record<string, number> | undefined = prior ? prior.stats.next_cursors : undefined;

  // Budget 0 / empty means "scan the entire topic": keep chaining until the
  // range is exhausted (more_available=false), the limit is hit, or Stop.
  const unlimited = budget <= 0;
  let scannedThisRun = 0;

  for (;;) {
    let callBudget: number;
    if (unlimited) {
      callBudget = PER_CALL_BUDGET;
    } else {
      const remaining = budget - scannedThisRun;
      if (remaining <= 0) return "budget";
      callBudget = remaining;
    }
    const req: SearchRequest = { ...baseReq, budget: callBudget, cursors };
    const r = await searchMessages(cluster, topic, req);
    const s = r.search;
    accMessages = [...accMessages, ...(r.messages ?? [])];
    accScanned += s.scanned;
    accMatched += s.matched;
    scannedThisRun += s.scanned;
    cursors = s.next_cursors;

    // Publish cumulative progress so the banner updates between calls.
    onProgress({
      messages: accMessages,
      stats: { ...s, scanned: accScanned, matched: accMatched },
      req,
    });

    if (!s.more_available) return "complete";
    if (stopOnLimit && accMatched >= limit) return "limit";
    if (shouldStop()) return "stopped";
    if (!madeProgress(s)) return "timeout";
  }
}
