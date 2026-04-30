import { useEffect, useMemo, useState } from "react";
import type { PathTree } from "@/lib/path-tree";

export interface PathSenseProps {
  tree: PathTree;
  value: string;
  onChange: (next: string) => void;
  onPick: (path: string, sampleValue: unknown) => void;
  placeholder?: string;
}

const TOP_N = 8;
const COMMON_BOOST = /(Id|Number|status|type|timestamp|createdAt|updatedAt)$/i;

interface Row {
  path: string;
  type: string;
  preview: string;
  sampleValue: unknown;
}

function previewOf(values: unknown[], distinctCount: number): string {
  if (values.length === 0) return "";
  if (distinctCount > values.length) return `${values.length}+ distinct`;
  if (distinctCount > 1) return `${distinctCount} distinct`;
  return JSON.stringify(values[0]);
}

function rank(path: string, type: string): number {
  let score = 0;
  if (type !== "object" && type !== "array") score += 5;
  const depth = (path.match(/\./g) || []).length;
  score -= depth;
  const tail = path.split(/[.\[]/).pop() ?? "";
  if (COMMON_BOOST.test(tail)) score += 3;
  return score;
}

function toRows(tree: PathTree): Row[] {
  const rows: Row[] = [];
  for (const [path, info] of tree) {
    rows.push({
      path,
      type: info.type,
      preview: previewOf(info.sampleValues, info.distinctCount),
      sampleValue: info.sampleValues.length > 0 ? info.sampleValues[0] : info.sampleValues,
    });
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
  placeholder = "Tippen oder ↓ für Top-Felder",
}: PathSenseProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState(value);

  // Sync local query with controlled value when it changes from the outside.
  useEffect(() => {
    setQuery(value);
  }, [value]);

  const allRows = useMemo(() => toRows(tree), [tree]);
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const base = q
      ? allRows.filter((r) => r.path.toLowerCase().includes(q))
      : allRows.slice(0, TOP_N);
    return base;
  }, [allRows, query]);

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Escape") {
      setOpen(false);
    } else if (e.key === "ArrowDown") {
      setOpen(true);
    } else if (e.key === "Tab") {
      if (/\[(\*|\d+)\]/.test(value)) {
        e.preventDefault();
        onChange(toggleArraySegment(value));
      }
    }
  };

  return (
    <div className="relative">
      <input
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
            <div className="p-2 text-xs text-muted">
              Sample ist kein JSON oder Topic ist leer — Pfad manuell eingeben.
            </div>
          ) : (
            <ul className="max-h-72 overflow-auto py-1 text-xs">
              {filtered.length === 0 ? (
                <li className="px-2 py-1 text-muted">Keine Treffer</li>
              ) : (
                filtered.map((r) => (
                  <li key={r.path}>
                    <button
                      type="button"
                      onClick={() => {
                        setQuery(r.path);
                        onChange(r.path);
                        onPick(r.path, r.sampleValue);
                        setOpen(false);
                      }}
                      className="flex w-full items-center justify-between gap-3 px-2 py-1 text-left hover:bg-accent-subtle"
                    >
                      <span className="font-mono">{r.path}</span>
                      <span className="text-muted">
                        {r.type}
                        {r.preview ? `  ${r.preview}` : ""}
                      </span>
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
