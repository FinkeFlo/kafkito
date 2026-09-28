import { useState } from "react";
import type { SearchDirection, SearchMode, SearchOp } from "@/lib/api";
import { useTimeRangeState } from "./use-time-range-state";

/** Inputs of the search panel. */
export function useSearchForm() {
  const [mode, setMode] = useState<SearchMode>("contains");
  const [path, setPath] = useState("");
  const [op, setOp] = useState<SearchOp>("contains");
  const [needle, setNeedle] = useState("");
  const range = useTimeRangeState();
  const [direction, setDirection] = useState<SearchDirection>("newest_first");
  const [stopOnLimit, setStopOnLimit] = useState(true);
  const [budget, setBudget] = useState(50000);
  const [budgetUnlimited, setBudgetUnlimited] = useState(false);

  return {
    mode,
    setMode,
    path,
    setPath,
    op,
    setOp,
    needle,
    setNeedle,
    range,
    direction,
    setDirection,
    stopOnLimit,
    setStopOnLimit,
    budget,
    setBudget,
    budgetUnlimited,
    setBudgetUnlimited,
  };
}

export type SearchForm = ReturnType<typeof useSearchForm>;
