// The message selected for compare ("Select for compare" on a message row):
// kept for the life of the browser tab so it survives switching topics and
// clusters, and read by the compare bar and by "Compare with selected".
// Holds a message reference only (cluster name, topic, partition, offset),
// never data or credentials.
import { useMemo, useSyncExternalStore } from "react";
import { formatMessageRef, type MessageRef, parseMessageRef } from "./message-reference";

const STORAGE_KEY = "kafkito.compareSelection";

// Used when sessionStorage throws (blocked site data, some private modes):
// the selection then lasts until the page is reloaded.
let memory: string | null = null;
const listeners = new Set<() => void>();

function read(): string | null {
  try {
    return window.sessionStorage.getItem(STORAGE_KEY);
  } catch {
    return memory;
  }
}

/** The reference text of the selected message, or null. */
export function getCompareSelection(): string | null {
  if (typeof window === "undefined") return null;
  return read();
}

export function setCompareSelection(ref: MessageRef | null): void {
  const text = ref ? formatMessageRef(ref) : null;
  memory = text;
  try {
    if (text === null) window.sessionStorage.removeItem(STORAGE_KEY);
    else window.sessionStorage.setItem(STORAGE_KEY, text);
  } catch {
    // Storage unavailable; `memory` above holds the selection.
  }
  for (const cb of listeners) cb();
}

export function subscribeCompareSelection(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/** The selected message, or null; re-renders when the selection changes. */
export function useCompareSelection(): MessageRef | null {
  const text = useSyncExternalStore(subscribeCompareSelection, getCompareSelection, () => null);
  return useMemo(() => (text ? parseMessageRef(text) : null), [text]);
}

/** Whether `a` and `b` name the same message. */
export function sameMessage(a: MessageRef | null, b: MessageRef | null): boolean {
  return !!a && !!b && formatMessageRef(a) === formatMessageRef(b);
}
