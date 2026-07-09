const STORAGE_KEY = "kafkito.whatsnew.lastSeen.v1";
const CHANGED_EVENT = "kafkito:whatsnew-changed";

/**
 * Strips a leading "v" and build-variant suffixes so the runtime version
 * from /api/v1/info (e.g. "0.0.0-rc17-btp", "v0.0.0-dev") matches the
 * CHANGELOG `version` keys (e.g. "0.0.0-rc17").
 */
export function normalizeVersion(v: string): string {
  return v.replace(/^v/, "").replace(/-(btp|local|dev)$/i, "");
}

export function getLastSeen(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export function markSeen(version: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, version);
    window.dispatchEvent(new CustomEvent(CHANGED_EVENT));
  } catch {
    /* quota or access denied — ignore */
  }
}

export function hasUnseen(
  current: string | undefined,
  lastSeen: string | null,
): boolean {
  return !!current && current !== lastSeen;
}

/** Subscribe to seen-state changes (other tabs or in-tab markSeen). */
export function subscribeWhatsNew(cb: () => void): () => void {
  if (typeof window === "undefined") return () => undefined;
  const onStorage = (e: StorageEvent) => {
    if (e.key === STORAGE_KEY) cb();
  };
  const onCustom = () => cb();
  window.addEventListener("storage", onStorage);
  window.addEventListener(CHANGED_EVENT, onCustom);
  return () => {
    window.removeEventListener("storage", onStorage);
    window.removeEventListener(CHANGED_EVENT, onCustom);
  };
}
