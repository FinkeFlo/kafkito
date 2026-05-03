import { useEffect, useState } from "react";

const STORAGE_KEY = "kafkito.timezone";
const EVENT = "kafkito:timezone-change";

export type TimeZoneMode = "utc" | "local";

function read(): TimeZoneMode {
  if (typeof window === "undefined") return "local";
  const raw = window.localStorage.getItem(STORAGE_KEY);
  return raw === "utc" ? "utc" : "local";
}

export function getTimeZone(): TimeZoneMode {
  return read();
}

export function setTimeZone(mode: TimeZoneMode): void {
  window.localStorage.setItem(STORAGE_KEY, mode);
  window.dispatchEvent(new CustomEvent(EVENT, { detail: mode }));
}

/**
 * Resolve the user's browser/OS IANA zone (e.g. "Europe/Berlin"). Used in
 * UI labels so users can see what "Local" actually maps to. Falls back to
 * "UTC" if Intl is unavailable or returns an empty string.
 */
export function getDetectedTimeZone(): string {
  if (typeof window === "undefined") return "UTC";
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/**
 * Subscribes to the global UTC ↔ local toggle. All <Timestamp /> instances must
 * read through this hook to stay in sync.
 */
export function useTimeZone(): [TimeZoneMode, (next: TimeZoneMode) => void] {
  const [mode, setMode] = useState<TimeZoneMode>(read);
  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<TimeZoneMode>).detail;
      setMode(detail);
    };
    const storage = (e: StorageEvent) => {
      if (e.key === STORAGE_KEY) setMode(read());
    };
    window.addEventListener(EVENT, handler);
    window.addEventListener("storage", storage);
    return () => {
      window.removeEventListener(EVENT, handler);
      window.removeEventListener("storage", storage);
    };
  }, []);
  return [mode, setTimeZone];
}
