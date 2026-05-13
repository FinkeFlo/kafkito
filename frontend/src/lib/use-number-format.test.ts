import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getDetectedLocale,
  getNumberFormat,
  resolveLocale,
  setNumberFormat,
  useNumberFormat,
} from "./use-number-format";

const nfKey = "kafkito.numberFormat";
const nfEvent = "kafkito:number-format-change";

afterEach(() => {
  window.localStorage.removeItem(nfKey);
  vi.restoreAllMocks();
});

describe("getNumberFormat", () => {
  it("defaults to 'auto' when localStorage is empty (C1)", () => {
    expect(getNumberFormat()).toBe("auto");
  });

  it.each<["auto" | "de" | "en"]>([["auto"], ["de"], ["en"]])(
    "returns the stored value verbatim for valid preference %p (C2)",
    (pref) => {
      window.localStorage.setItem(nfKey, pref);

      expect(getNumberFormat()).toBe(pref);
    },
  );

  it.each<[string]>([
    ["DE"],
    ["de-DE"],
    ["fr"],
    ["garbage"],
    [""],
  ])(
    "falls back to 'auto' for any non-whitelisted value (%p) (C3 / M-strict mutation-guard)",
    (raw) => {
      window.localStorage.setItem(nfKey, raw);

      expect(getNumberFormat()).toBe("auto");
    },
  );
});

describe("setNumberFormat", () => {
  it("persists the preference and dispatches the kafkito:number-format-change CustomEvent (C4)", () => {
    let receivedDetail: unknown = null;
    const handler = (e: Event) => {
      receivedDetail = (e as CustomEvent).detail;
    };
    window.addEventListener(nfEvent, handler);

    try {
      setNumberFormat("de");

      expect(window.localStorage.getItem(nfKey)).toBe("de");
      expect(receivedDetail).toBe("de");
    } finally {
      window.removeEventListener(nfEvent, handler);
    }
  });
});

describe("resolveLocale", () => {
  it("maps 'de' to 'de-DE' (C5a)", () => {
    expect(resolveLocale("de")).toBe("de-DE");
  });

  it("maps 'en' to 'en-US' (C5b)", () => {
    expect(resolveLocale("en")).toBe("en-US");
  });

  it("delegates 'auto' to the detected browser locale (C5c)", () => {
    vi.spyOn(window.navigator, "language", "get").mockReturnValue("fr-FR");

    expect(resolveLocale("auto")).toBe("fr-FR");
  });
});

describe("getDetectedLocale", () => {
  it("returns the navigator.language value when present (C6)", () => {
    vi.spyOn(window.navigator, "language", "get").mockReturnValue("it-IT");

    expect(getDetectedLocale()).toBe("it-IT");
  });

  it("falls back to 'en-US' when navigator.language is empty (C7 / M-empty guard)", () => {
    vi.spyOn(window.navigator, "language", "get").mockReturnValue("");

    expect(getDetectedLocale()).toBe("en-US");
  });
});

describe("useNumberFormat", () => {
  it("seeds preference + effectiveLocale from localStorage on first render (C8)", () => {
    window.localStorage.setItem(nfKey, "de");

    const { result } = renderHook(() => useNumberFormat());

    expect(result.current.preference).toBe("de");
    expect(result.current.effectiveLocale).toBe("de-DE");
  });

  it("re-renders subscribers when setNumberFormat is called from the same window (C9 / M-cust + M-dispatch guard)", () => {
    const { result } = renderHook(() => useNumberFormat());
    expect(result.current.preference).toBe("auto");

    act(() => {
      setNumberFormat("en");
    });

    expect(result.current.preference).toBe("en");
    expect(result.current.effectiveLocale).toBe("en-US");
  });

  it("re-renders subscribers when a foreign tab dispatches a 'storage' event for kafkito.numberFormat (C10 / M-storage guard)", () => {
    const { result } = renderHook(() => useNumberFormat());
    expect(result.current.preference).toBe("auto");

    act(() => {
      window.localStorage.setItem(nfKey, "de");
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: nfKey,
          oldValue: null,
          newValue: "de",
        }),
      );
    });

    expect(result.current.preference).toBe("de");
  });

  it("ignores 'storage' events for unrelated keys (C11 / M-filter mutation-guard)", () => {
    window.localStorage.setItem(nfKey, "de");
    const { result } = renderHook(() => useNumberFormat());
    expect(result.current.preference).toBe("de");

    window.localStorage.setItem(nfKey, "en");
    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "some.other.key",
          oldValue: null,
          newValue: "anything",
        }),
      );
    });

    expect(result.current.preference).toBe("de");
  });

  it("setPreference exposed by the hook routes through setNumberFormat (C12)", () => {
    const { result } = renderHook(() => useNumberFormat());

    act(() => {
      result.current.setPreference("de");
    });

    expect(window.localStorage.getItem(nfKey)).toBe("de");
    expect(result.current.preference).toBe("de");
  });
});
