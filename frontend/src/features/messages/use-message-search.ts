import { useRef, useState } from "react";
import type { SearchRequest } from "@/lib/api";
import { runSearchChain, type SearchResult, type SearchStopReason } from "./search-chain";
import { computeTimeRange } from "./time-range";
import type { SearchForm } from "./use-search-form";

/** Runs the search panel's query and holds its result, error and progress. */
export function useMessageSearch(
  cluster: string,
  topic: string,
  partition: number,
  limit: number,
  form: SearchForm,
) {
  const [searching, setSearching] = useState(false);
  const [searchResult, setSearchResult] = useState<SearchResult | null>(null);
  const [searchError, setSearchError] = useState<string | null>(null);
  // Why the most recent (auto-chained) search stopped. Drives the result banner.
  const [searchStopReason, setSearchStopReason] = useState<SearchStopReason | null>(null);
  // Set to true to abort an in-flight auto-chain between continuation calls.
  const stopSearchRef = useRef(false);

  const runSearch = async (continueChain = false) => {
    stopSearchRef.current = false;
    setSearching(true);
    setSearchError(null);
    // A fresh search clears any previous result immediately so the list shows
    // search results (empty until the first match arrives) rather than the
    // browse messages while scanning is still in progress.
    if (!continueChain) {
      setSearchResult(null);
      setSearchStopReason(null);
    }
    const { mode, path, op, needle, range } = form;
    const { from_ts_ms, to_ts_ms } = computeTimeRange(
      range.mode,
      range.preset,
      range.customFrom,
      range.customTo,
    );
    const baseReq: SearchRequest = {
      partition,
      limit,
      direction: form.direction,
      stop_on_limit: form.stopOnLimit,
      mode,
      path: mode === "contains" || mode === "js" ? "" : path,
      op: mode === "contains" || mode === "js" ? "contains" : op,
      value: needle,
      zones: mode === "contains" ? ["value", "key", "headers"] : ["value"],
      from_ts_ms,
      to_ts_ms,
    };

    try {
      const reason = await runSearchChain({
        cluster,
        topic,
        baseReq,
        prior: continueChain ? searchResult : null,
        budget: form.budgetUnlimited ? 0 : form.budget,
        stopOnLimit: form.stopOnLimit,
        limit,
        shouldStop: () => stopSearchRef.current,
        onProgress: setSearchResult,
      });
      setSearchStopReason(reason);
    } catch (err) {
      setSearchError((err as Error).message);
    } finally {
      setSearching(false);
      stopSearchRef.current = false;
    }
  };

  const stopSearch = () => {
    stopSearchRef.current = true;
  };

  const clearSearch = () => {
    setSearchResult(null);
    setSearchError(null);
    setSearchStopReason(null);
  };

  return {
    searching,
    searchResult,
    searchError,
    searchStopReason,
    inSearchMode: searchResult !== null || searching,
    runSearch,
    stopSearch,
    clearSearch,
  };
}

export type MessageSearch = ReturnType<typeof useMessageSearch>;
