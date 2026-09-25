import { useEffect, useMemo, useRef, useState } from "react";
import type { PathTree } from "@/lib/path-tree";
import { useFuzzy } from "@/lib/fuzzy";
import { Highlight } from "@/components/highlight";

export interface PathSenseProps {
  tree: PathTree;
  value: string;
  onChange: (next: string) => void;
  onPick: (path: string, type: string) => void;
  placeholder?: string;
  /** Applied to the inner combobox input so an external <label htmlFor> can target it. */
  id?: string;
  /** Shown in the dropdown when `tree` is empty. Defaults to JSONPath's
   * copy; XPath mode passes its own XML-flavored message. */
  emptyMessage?: string;
  /**
   * Enables the Tab shortcut that flips the last array segment between a
   * numeric index and `[*]`. JSONPath-only: in XPath `item[2]` and `item[*]`
   * are both valid but mean unrelated things (`[*]` selects elements having
   * a child *element*), so rewriting one into the other would silently
   * change the query's meaning — and the `preventDefault` would trap focus
   * in the input. XPath mode passes `false`.
   */
  arrayIndexToggle?: boolean;
}

const TOP_N = 8;
const COMMON_BOOST = /(Id|Number|status|type|timestamp|createdAt|updatedAt)$/i;

interface Row {
  path: string;
  type: string;
}

function rank(path: string, type: string): number {
  let score = 0;
  if (type !== "object" && type !== "array") score += 5;
  // Separators cover both JSONPath (`$.order.items[*].sku`) and XPath
  // (`//order/items/item/@sku`); without `/` every XPath row would score an
  // identical depth of 0 and the shallow-first ordering would collapse.
  const depth = (path.match(/[./]/g) || []).length;
  score -= depth;
  const tail = path.split(/[.[/]/).pop() ?? "";
  if (COMMON_BOOST.test(tail)) score += 3;
  return score;
}

function toRows(tree: PathTree): Row[] {
  const rows: Row[] = [];
  for (const [path, info] of tree) {
    rows.push({ path, type: info.type });
  }
  rows.sort((a, b) => rank(b.path, b.type) - rank(a.path, a.type));
  return rows;
}

function toggleArraySegment(path: string): string {
  // toggle the last [N] or [*] segment between numeric and *
  const lastIndex = path.lastIndexOf("[");
  if (lastIndex < 0) return path;
  const close = path.indexOf("]", lastIndex);
  if (close < 0) return path;
  const inner = path.slice(lastIndex + 1, close);
  const replacement = inner === "*" ? "0" : "*";
  return path.slice(0, lastIndex + 1) + replacement + path.slice(close);
}

export function PathSense({
  tree,
  value,
  onChange,
  onPick,
  placeholder = "Type or ↓ for top fields",
  id,
  emptyMessage = "Sample isn't JSON or topic is empty — enter path manually.",
  arrayIndexToggle = true,
}: PathSenseProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState(value);
  const rootRef = useRef<HTMLDivElement>(null);

  // Sync local query with controlled value when it changes from the outside.
  useEffect(() => {
    setQuery(value);
  }, [value]);

  const allRows = useMemo(() => toRows(tree), [tree]);
  // Token-wise substring matching over the whole path: every whitespace-
  // separated token of the query must appear somewhere in the path, in any
  // order (so "order price" finds "$.order.items[*].price", as does "pric").
  // This is the same Fuse-based helper the Command Palette and topic list
  // use, so behavior and highlighting are consistent across the app. Note
  // that it is *not* typo-tolerant — lib/fuzzy.ts pins Fuse to threshold 0
  // and exact tokens, so "pirce" finds nothing. Ranking by rank() only
  // applies with no active query; an active query defers to Fuse's own
  // relevance ordering.
  const fuzzy = useFuzzy(allRows, { keys: ["path"], query });
  const filtered = useMemo(
    () => (query.trim() ? fuzzy.results : allRows.slice(0, TOP_N)),
    [fuzzy.results, allRows, query],
  );

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Escape") {
      setOpen(false);
    } else if (e.key === "ArrowDown") {
      setOpen(true);
    } else if (e.key === "Tab") {
      if (arrayIndexToggle && /\[(\*|\d+)\]/.test(query)) {
        e.preventDefault();
        onChange(toggleArraySegment(query));
      }
    }
  };

  return (
    <div
      ref={rootRef}
      className="relative"
      onBlur={(e) => {
        if (!rootRef.current?.contains(e.relatedTarget as Node | null)) {
          setOpen(false);
        }
      }}
    >
      <input
        id={id}
        role="combobox"
        aria-expanded={open}
        value={query}
        placeholder={placeholder}
        onChange={(e) => {
          setQuery(e.target.value);
          onChange(e.target.value);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKey}
        className="w-full rounded border border-border bg-panel px-2 py-1 font-mono text-xs"
      />
      {open && (
        <div className="absolute z-10 mt-1 w-full rounded-md border border-border bg-panel shadow-lg">
          {allRows.length === 0 ? (
            <div className="p-2 text-xs text-muted">{emptyMessage}</div>
          ) : (
            <ul className="max-h-72 overflow-auto py-1 text-xs">
              {filtered.length === 0 ? (
                <li className="px-2 py-1 text-muted">No matches</li>
              ) : (
                filtered.map((r) => (
                  <li key={r.path}>
                    <button
                      type="button"
                      onClick={() => {
                        setQuery(r.path);
                        onChange(r.path);
                        onPick(r.path, r.type);
                        setOpen(false);
                      }}
                      className="flex w-full items-center gap-3 px-2 py-1 text-left hover:bg-accent-subtle"
                    >
                      {/* Path plus type only. The type is a literal from a
                          fixed set, never payload from the message, and it is
                          what drives ranking and the operator prefill. */}
                      <span className="min-w-0 flex-1 truncate font-mono" title={r.path}>
                        <Highlight text={r.path} ranges={fuzzy.rangesFor(r, "path")} />
                      </span>
                      <span className="shrink-0 text-muted">{r.type}</span>
                    </button>
                  </li>
                ))
              )}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
