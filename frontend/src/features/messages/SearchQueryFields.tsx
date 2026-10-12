import type { SearchMode, SearchOp } from "@/lib/api";
import { MAX_HYDRATE_VALUE_BYTES } from "@/lib/hydrate-sample";
import { useFormatters } from "@/lib/use-formatters";
import { HeaderKeySense } from "./HeaderKeySense";
import { PathSense } from "./PathSense";
import type { HeaderKeyStat } from "./header-keys";
import type { PathSuggestions } from "./use-path-suggestions";
import type { SearchForm } from "./use-search-form";

/** Mode, path, header key, operator and value inputs of the search panel. */
export function SearchQueryFields({
  form,
  suggestions,
  headerKeys,
}: {
  form: SearchForm;
  suggestions: PathSuggestions;
  /** Header keys of the loaded messages and how many messages that is. */
  headerKeys: { stats: HeaderKeyStat[]; total: number };
}) {
  const fmt = useFormatters();
  const { mode, setMode, path, setPath, headerKey, setHeaderKey, op, setOp, needle, setNeedle } =
    form;
  const { pathTree, xmlPathTree, sampleTooLargeToScan } = suggestions;
  // Every mode but text and JavaScript compares with an operator; exists and
  // missing check presence only and need no value.
  const usesOperator = mode !== "contains" && mode !== "js";
  const valueIgnored = usesOperator && (op === "exists" || op === "missing");

  return (
    <>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <label className="font-medium" htmlFor="search-mode">
          Mode
        </label>
        <select
          id="search-mode"
          value={mode}
          onChange={(e) => setMode(e.target.value as SearchMode)}
          className="rounded border border-border bg-panel px-2 py-1"
        >
          <option value="contains">Text contains</option>
          <option value="key">Record key</option>
          <option value="header">Header</option>
          <option value="jsonpath">JSONPath</option>
          <option value="xpath">XPath</option>
          <option value="js">JavaScript</option>
        </select>

        {(mode === "jsonpath" || mode === "xpath") && (
          <>
            <label className="font-medium" htmlFor="search-path">
              Path
            </label>
            {/* Both structured modes get PathSense; only the suggestion
                tree and its XML/JSON-flavored copy differ. */}
            <div className="w-56">
              <PathSense
                id="search-path"
                tree={mode === "xpath" ? xmlPathTree : pathTree}
                value={path}
                onChange={setPath}
                placeholder={mode === "xpath" ? "//order/@status" : "Type or ↓ for top fields"}
                emptyMessage={
                  sampleTooLargeToScan
                    ? `Sample is larger than ${fmt.bytes(MAX_HYDRATE_VALUE_BYTES)} — too large to scan for field names. Enter path manually.`
                    : mode === "xpath"
                      ? "Sample isn't XML or topic is empty — enter path manually."
                      : "Sample isn't JSON or topic is empty — enter path manually."
                }
                arrayIndexToggle={mode !== "xpath"}
                onPick={(picked, type) => {
                  setPath(picked);
                  // The tree carries field names only, so the value is
                  // yours to type. A container can't be compared with
                  // `eq` at all, so it gets `exists` semantics.
                  const isScalar = type !== "object" && type !== "array";
                  setOp(isScalar ? "eq" : "exists");
                  setNeedle("");
                }}
              />
            </div>
          </>
        )}
        {mode === "header" && (
          <>
            <label className="font-medium" htmlFor="search-header-key">
              Header key
            </label>
            <div className="w-56">
              <HeaderKeySense
                id="search-header-key"
                value={headerKey}
                onChange={setHeaderKey}
                stats={headerKeys.stats}
                total={headerKeys.total}
              />
            </div>
          </>
        )}
        {usesOperator && (
          <>
            <label className="font-medium" htmlFor="search-operator">
              Operator
            </label>
            <select
              id="search-operator"
              value={op}
              onChange={(e) => setOp(e.target.value as SearchOp)}
              className="rounded border border-border bg-panel px-2 py-1"
            >
              <option value="exists">exists</option>
              <option value="missing">missing</option>
              <option value="eq">=</option>
              <option value="ne">≠</option>
              <option value="contains">contains</option>
              <option value="regex">regex</option>
              <option value="gt">&gt;</option>
              <option value="gte">≥</option>
              <option value="lt">&lt;</option>
              <option value="lte">≤</option>
            </select>
          </>
        )}
        <label
          className="font-medium"
          htmlFor={mode === "js" ? "search-expression" : "search-value"}
        >
          {mode === "js" ? "Expression" : "Value"}
        </label>
        {mode === "js" ? (
          <textarea
            id="search-expression"
            value={needle}
            onChange={(e) => setNeedle(e.target.value)}
            placeholder={'parsed && parsed.amount > 1000 && key.startsWith("ord-")'}
            className="min-h-[2.2rem] w-full flex-1 rounded border border-border bg-panel px-2 py-1 font-mono"
            rows={2}
          />
        ) : (
          <input
            id="search-value"
            value={needle}
            onChange={(e) => setNeedle(e.target.value)}
            placeholder={
              mode === "contains"
                ? "Substring"
                : valueIgnored
                  ? "(ignored)"
                  : "e.g. 42 / shipped / ^A.*"
            }
            className="w-56 rounded border border-border bg-panel px-2 py-1 font-mono"
            disabled={valueIgnored}
            aria-describedby={valueIgnored ? "search-value-ignored" : undefined}
          />
        )}
        {valueIgnored && (
          <span id="search-value-ignored" className="sr-only">
            Not needed: {op} checks presence only.
          </span>
        )}
      </div>
      {mode === "key" && (
        <div className="rounded border border-border bg-panel p-2 text-[11px] text-muted">
          Compares the record key as the list shows it. <code className="font-mono">exists</code>{" "}
          finds records with a key. <SearchDocsLink />
        </div>
      )}
      {mode === "header" && (
        <div className="rounded border border-border bg-panel p-2 text-[11px] text-muted">
          Compares the value of one header. The header key matches exactly, case-sensitive; a record
          without the header matches <code className="font-mono">missing</code> only.{" "}
          <SearchDocsLink />
        </div>
      )}
      {mode === "jsonpath" && (
        <div className="rounded border border-border bg-panel p-2 text-[11px] text-muted">
          Example: <code className="font-mono">$.order.orderNumber</code>. <SearchDocsLink />
        </div>
      )}
      {mode === "xpath" && (
        <div className="rounded border border-border bg-panel p-2 text-[11px] text-muted">
          Example: <code className="font-mono">{"/order/orderNumber"}</code>. <SearchDocsLink />
        </div>
      )}
      {mode === "js" && (
        <div className="rounded border border-border bg-panel p-2 text-[11px] text-muted">
          Variables: <code className="font-mono">key</code>,{" "}
          <code className="font-mono">value</code> (string),{" "}
          <code className="font-mono">parsed</code> (JSON),{" "}
          <code className="font-mono">headers</code>, <code className="font-mono">partition</code>,{" "}
          <code className="font-mono">offset</code>, <code className="font-mono">timestampMs</code>.
          Example:{" "}
          <code className="font-mono">
            parsed &amp;&amp; parsed.currency === "EUR" &amp;&amp; parsed.amount &gt; 500
          </code>
          . Limit 100 ms per message. <SearchDocsLink />
        </div>
      )}
    </>
  );
}

const SEARCH_DOCS_URL =
  "https://finkeflo.github.io/kafkito/ui/workflows/#workflow-1-find-a-message-in-a-topic";

function SearchDocsLink() {
  return (
    <a
      href={SEARCH_DOCS_URL}
      target="_blank"
      rel="noopener noreferrer"
      className="text-accent hover:underline"
    >
      More examples
    </a>
  );
}
