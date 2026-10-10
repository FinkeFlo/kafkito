import type { SearchDirection } from "@/lib/api";
import { useFormatters } from "@/lib/use-formatters";
import type { SearchForm } from "./use-search-form";

/** Direction, stop and budget options plus the Search / Stop / Clear buttons. */
export function SearchRunBar({
  form,
  limit,
  searching,
  hasResult,
  onSearch,
  onStop,
  onClear,
}: {
  form: SearchForm;
  limit: number;
  searching: boolean;
  hasResult: boolean;
  onSearch: () => void;
  onStop: () => void;
  onClear: () => void;
}) {
  const fmt = useFormatters();
  const {
    mode,
    path,
    needle,
    direction,
    setDirection,
    stopOnLimit,
    setStopOnLimit,
    budget,
    setBudget,
    budgetUnlimited,
    setBudgetUnlimited,
  } = form;

  return (
    <div className="flex flex-wrap items-center gap-3 text-xs">
      <div className="flex items-center gap-1.5">
        <label className="font-medium" htmlFor="search-direction">
          Direction
        </label>
        <select
          id="search-direction"
          value={direction}
          onChange={(e) => setDirection(e.target.value as SearchDirection)}
          className="rounded border border-border bg-panel px-2 py-1"
        >
          <option value="newest_first">new → old</option>
          <option value="oldest_first">old → new</option>
        </select>
      </div>
      <label className="flex items-center gap-1.5">
        <input
          type="checkbox"
          checked={stopOnLimit}
          onChange={(e) => setStopOnLimit(e.target.checked)}
          className="h-3.5 w-3.5"
        />
        Stop after Limit matches ({fmt.number(limit)})
      </label>
      <label className="flex items-center gap-1.5">
        <input
          type="checkbox"
          checked={budgetUnlimited}
          onChange={(e) => setBudgetUnlimited(e.target.checked)}
          className="h-3.5 w-3.5"
        />
        Scan whole topic
      </label>
      <div className="flex items-center gap-1.5">
        <label
          htmlFor="search-budget"
          className={`font-medium ${budgetUnlimited ? "text-subtle-text" : ""}`}
        >
          Max messages to scan
        </label>
        <input
          id="search-budget"
          type="number"
          min={1}
          max={1000000}
          step={1000}
          value={budget === 0 ? "" : budget}
          disabled={budgetUnlimited}
          placeholder="50000"
          title="Max number of messages to scan per search. Enable 'Scan whole topic' to scan everything."
          onChange={(e) => setBudget(e.target.value === "" ? 0 : Number(e.target.value) || 0)}
          className="w-24 rounded border border-border bg-panel px-2 py-1 disabled:opacity-50"
        />
      </div>
      <button
        type="button"
        onClick={onSearch}
        disabled={
          searching ||
          (mode === "js" && needle.trim() === "") ||
          (mode !== "contains" && mode !== "js" && path.trim() === "")
        }
        className="rounded bg-accent px-3 py-1 font-semibold text-accent-foreground hover:bg-accent-hover disabled:opacity-50"
      >
        {searching ? "Searching…" : "Search"}
      </button>
      {searching && (
        <button
          type="button"
          onClick={onStop}
          className="rounded border border-border bg-panel px-2 py-1 hover:border-border-hover"
        >
          Stop
        </button>
      )}
      {hasResult && !searching && (
        <button
          type="button"
          onClick={onClear}
          className="rounded border border-border bg-panel px-2 py-1 hover:border-border-hover"
        >
          Clear result
        </button>
      )}
    </div>
  );
}
