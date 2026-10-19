import { useEffect, useState } from "react";
import type { SearchMode, SearchOp } from "@/lib/api";
import { buildJsonPath, wildcardArrayIndices, type Token } from "@/lib/path-builder";
import type { SearchForm } from "./use-search-form";

/**
 * A click on the key or a header of a message: search for that key, for a
 * header with that value, or (without a value) for records carrying the
 * header.
 */
export type FieldFilter =
  | { kind: "key"; value: string }
  | { kind: "header"; key: string; value?: string };

type UndoToast = {
  previous: { mode: SearchMode; path: string; headerKey: string; op: SearchOp; needle: string };
  until: number;
};

/**
 * Click-to-filter: a click on a value in a message turns it into a JSONPath
 * search, a click on the key or a header into a key or header search, with a
 * short-lived undo when it replaced earlier input.
 */
export function useClickToFilter(form: SearchForm, openSearch: () => void) {
  const { mode, path, headerKey, op, needle, setMode, setPath, setHeaderKey, setOp, setNeedle } =
    form;
  const [undoToast, setUndoToast] = useState<UndoToast | null>(null);

  // Applies a click's search and offers undo when it replaced typed input.
  const replace = (apply: () => void) => {
    const previous = { mode, path, headerKey, op, needle };
    const hadAnyInput = [path, headerKey, needle].some((v) => v.trim() !== "");
    openSearch();
    apply();
    if (hadAnyInput) {
      setUndoToast({ previous, until: Date.now() + 4000 });
    }
  };

  useEffect(() => {
    if (!undoToast) return;
    const remaining = undoToast.until - Date.now();
    if (remaining <= 0) {
      setUndoToast(null);
      return;
    }
    const timer = setTimeout(() => setUndoToast(null), remaining);
    return () => clearTimeout(timer);
  }, [undoToast]);

  const handlePick = (trail: Token[], leafValue: unknown) =>
    replace(() => {
      setMode("jsonpath");
      setPath(buildJsonPath(wildcardArrayIndices(trail)));
      if (leafValue !== undefined) {
        setOp("eq");
        setNeedle(String(leafValue));
      } else {
        setOp("exists");
        setNeedle("");
      }
    });

  const handleFilter = (f: FieldFilter) =>
    replace(() => {
      setMode(f.kind);
      if (f.kind === "header") setHeaderKey(f.key);
      if (f.value !== undefined) {
        setOp("eq");
        setNeedle(f.value);
      } else {
        setOp("exists");
        setNeedle("");
      }
    });

  const undo = () => {
    if (!undoToast) return;
    const { previous } = undoToast;
    setMode(previous.mode);
    setPath(previous.path);
    setHeaderKey(previous.headerKey);
    setOp(previous.op);
    setNeedle(previous.needle);
    setUndoToast(null);
  };

  return { showUndo: undoToast !== null, handlePick, handleFilter, undo };
}
