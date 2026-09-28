import { StatusBox } from "@/components/ui/StatusIcon";
import { SearchQueryFields } from "./SearchQueryFields";
import { SearchRangeFields } from "./SearchRangeFields";
import { SearchResultBanner } from "./SearchResultBanner";
import { SearchRunBar } from "./SearchRunBar";
import type { MessageSearch } from "./use-message-search";
import type { PathSuggestions } from "./use-path-suggestions";
import type { SearchForm } from "./use-search-form";

/** The search panel below the toolbar: query inputs, run controls, result banner. */
export function SearchPanel({
  form,
  suggestions,
  search,
  limit,
  showUndo,
  onUndo,
}: {
  form: SearchForm;
  suggestions: PathSuggestions;
  search: MessageSearch;
  limit: number;
  /** A click-to-filter replaced earlier input and can still be undone. */
  showUndo: boolean;
  onUndo: () => void;
}) {
  const { searching, searchResult, searchError, searchStopReason } = search;

  return (
    <div className="space-y-3 border-b border-[var(--color-border)] bg-[var(--color-surface-subtle)] p-3">
      {showUndo && (
        <div className="flex items-center gap-3 rounded border border-border bg-panel p-2 text-xs">
          <span>Path replaced by click.</span>
          <button
            type="button"
            onClick={onUndo}
            className="rounded border border-border px-2 py-0.5 hover:border-border-strong"
          >
            Undo
          </button>
        </div>
      )}
      <SearchQueryFields form={form} suggestions={suggestions} />

      <SearchRangeFields range={form.range} />

      <SearchRunBar
        form={form}
        limit={limit}
        searching={searching}
        hasResult={!!searchResult}
        onSearch={() => search.runSearch()}
        onStop={search.stopSearch}
        onClear={search.clearSearch}
      />

      {searchError && <StatusBox intent="danger">{searchError}</StatusBox>}
      {searchResult && (
        <SearchResultBanner
          result={searchResult}
          searching={searching}
          stopReason={searchStopReason}
          onSearchMore={() => search.runSearch(true)}
        />
      )}
    </div>
  );
}
