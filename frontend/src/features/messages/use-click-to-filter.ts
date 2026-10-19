import { useEffect, useState } from "react";
import type { SearchMode, SearchOp } from "@/lib/api";
import { buildJsonPath, wildcardArrayIndices, type Token } from "@/lib/path-builder";
import type { SearchForm } from "./use-search-form";

/** A click on the key of a message: search for records with that key. */
export type FieldFilter = { kind: "key"; value: string };

type UndoToast = {
  previous: { mode: SearchMode; path: string; headerKey: string; op: SearchOp; needle: string };
  until: number;
};

/**
 * Click-to-filter: a click on a value in a message turns it into a JSONPath
 * search, a click on the key into a key search, with a
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
      setOp("eq");
      setNeedle(f.value);
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
