import { useCallback, useEffect, useState } from "react";

const STORAGE_KEY = "kafkito.numberFormat";
const EVENT = "kafkito:number-format-change";

export type NumberFormatPreference = "auto" | "de" | "en";

const FALLBACK_LOCALE = "en-US";

function read(): NumberFormatPreference {
  if (typeof window === "undefined") return "auto";
  const raw = window.localStorage.getItem(STORAGE_KEY);
  return raw === "de" || raw === "en" || raw === "auto" ? raw : "auto";
}

export function getNumberFormat(): NumberFormatPreference {
  return read();
}

export function setNumberFormat(pref: NumberFormatPreference): void {
  window.localStorage.setItem(STORAGE_KEY, pref);
  window.dispatchEvent(new CustomEvent(EVENT, { detail: pref }));
}

/**
 * Resolve the user's browser locale (e.g. "de-DE", "en-US"). Used both as the
 * value for the "auto" preference and as the hint shown in the user menu so
 * users see what "Auto" will actually pick. Falls back to "en-US" if Intl is
 * unavailable or returns nothing usable.
 */
export function getDetectedLocale(): string {
  if (typeof window === "undefined") return FALLBACK_LOCALE;
  try {
    const detected = window.navigator?.language;
    if (typeof detected === "string" && detected.length > 0) return detected;
  } catch {
    /* fall through */
  }
  return FALLBACK_LOCALE;
}

export function resolveLocale(pref: NumberFormatPreference): string {
  if (pref === "de") return "de-DE";
  if (pref === "en") return "en-US";
  return getDetectedLocale();
}

/**
 * Subscribes to the global number-format preference. All UI that formats
 * numbers must read through this hook (or `useFormatters`) to stay in sync
 * with the user-menu toggle and with foreign-tab changes.
 */
export function useNumberFormat(): {
  preference: NumberFormatPreference;
  effectiveLocale: string;
  setPreference: (next: NumberFormatPreference) => void;
} {
  const [preference, setPreferenceState] = useState<NumberFormatPreference>(read);

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<NumberFormatPreference>).detail;
      setPreferenceState(detail);
    };
    const storage = (e: StorageEvent) => {
      if (e.key === STORAGE_KEY) setPreferenceState(read());
    };
    window.addEventListener(EVENT, handler);
    window.addEventListener("storage", storage);
    return () => {
      window.removeEventListener(EVENT, handler);
      window.removeEventListener("storage", storage);
    };
  }, []);

  const setPreference = useCallback((next: NumberFormatPreference) => {
    setNumberFormat(next);
  }, []);

  return {
    preference,
    effectiveLocale: resolveLocale(preference),
    setPreference,
  };
}
