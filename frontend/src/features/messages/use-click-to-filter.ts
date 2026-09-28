import { useEffect, useState } from "react";
import type { SearchOp } from "@/lib/api";
import { buildJsonPath, wildcardArrayIndices, type Token } from "@/lib/path-builder";
import type { SearchForm } from "./use-search-form";

type UndoToast = {
  previous: { path: string; op: SearchOp; needle: string };
  until: number;
};

/**
 * Click-to-filter: a click on a value in a message turns it into a JSONPath
 * search, with a short-lived undo when it replaced earlier input.
 */
export function useClickToFilter(form: SearchForm, openSearch: () => void) {
  const { path, op, needle, setMode, setPath, setOp, setNeedle } = form;
  const [undoToast, setUndoToast] = useState<UndoToast | null>(null);

  const finalizePick = (trail: Token[], leafValue: unknown) => {
    const previous = { path, op, needle };
    const hadAnyInput = path.trim() !== "" || needle.trim() !== "";

    setMode("jsonpath");
    setPath(buildJsonPath(trail));
    if (leafValue !== undefined) {
      setOp("eq");
      setNeedle(String(leafValue));
    } else {
      setOp("exists");
      setNeedle("");
    }

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

  const handlePick = (trail: Token[], leafValue: unknown) => {
    openSearch();
    finalizePick(wildcardArrayIndices(trail), leafValue);
  };

  const undo = () => {
    if (!undoToast) return;
    setPath(undoToast.previous.path);
    setOp(undoToast.previous.op);
    setNeedle(undoToast.previous.needle);
    setUndoToast(null);
  };

  return { showUndo: undoToast !== null, handlePick, undo };
}
