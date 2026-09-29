import { useCallback, useState } from "react";
import type { Message, SearchDirection, SearchMode, SearchOp } from "@/lib/api";
import { detectSearchMode } from "./search-mode-detect";
import { useTimeRangeState } from "./use-time-range-state";

/**
 * Inputs of the search panel. `topic` scopes the manual-mode flag: once the
 * user picks a mode for a topic, `preselectMode` leaves it alone until
 * another topic is shown. A path or value already typed also keeps the
 * current mode, so reopening the panel never reinterprets it.
 */
export function useSearchForm(topic: string) {
  const [mode, setModeState] = useState<SearchMode>("contains");
  const [path, setPath] = useState("");
  const [op, setOp] = useState<SearchOp>("contains");
  const [needle, setNeedle] = useState("");
  const [modeTouchedFor, setModeTouchedFor] = useState<string | null>(null);
  const modeTouched = modeTouchedFor === topic || path !== "" || needle !== "";
  const setMode = useCallback(
    (next: SearchMode) => {
      setModeTouchedFor(topic);
      setModeState(next);
    },
    [topic],
  );
  /** Picks the mode that fits the given (already loaded) messages. */
  const preselectMode = useCallback(
    (messages: Pick<Message, "value_encoding">[]) => {
      if (!modeTouched) setModeState(detectSearchMode(messages));
    },
    [modeTouched],
  );
  const range = useTimeRangeState();
  const [direction, setDirection] = useState<SearchDirection>("newest_first");
  const [stopOnLimit, setStopOnLimit] = useState(true);
  const [budget, setBudget] = useState(50000);
  const [budgetUnlimited, setBudgetUnlimited] = useState(false);

  return {
    mode,
    setMode,
    preselectMode,
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
