import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { getTimeZone, setTimeZone, useTimeZone } from "./use-timezone";

const tzKey = "kafkito.timezone";
const tzEvent = "kafkito:timezone-change";

afterEach(() => {
  window.localStorage.removeItem(tzKey);
});

describe("getTimeZone", () => {
  it("defaults to 'local' when localStorage is empty (C1)", () => {
    expect(getTimeZone()).toBe("local");
  });

  it("returns 'utc' when localStorage holds the literal 'utc' (C2)", () => {
    window.localStorage.setItem(tzKey, "utc");

    expect(getTimeZone()).toBe("utc");
  });

  it.each<[string]>([
    ["UTC"],
    ["America/New_York"],
    ["garbage"],
    [""],
  ])("falls back to 'local' for any non-'utc' stored value (%p) (C3 / M-strict mutation-guard)", (raw) => {
    window.localStorage.setItem(tzKey, raw);

    expect(getTimeZone()).toBe("local");
  });
});

describe("setTimeZone", () => {
  it("persists the mode in localStorage and dispatches the kafkito:timezone-change CustomEvent (C4)", () => {
    let receivedDetail: unknown = null;
    const handler = (e: Event) => {
      receivedDetail = (e as CustomEvent).detail;
    };
    window.addEventListener(tzEvent, handler);

    try {
      setTimeZone("utc");

      expect(window.localStorage.getItem(tzKey)).toBe("utc");
      expect(receivedDetail).toBe("utc");
    } finally {
      window.removeEventListener(tzEvent, handler);
    }
  });
});

describe("useTimeZone", () => {
  it("seeds initial value from localStorage on first render (C5)", () => {
    window.localStorage.setItem(tzKey, "utc");

    const { result } = renderHook(() => useTimeZone());

    expect(result.current[0]).toBe("utc");
  });

  it("re-renders subscribers when setTimeZone is called from the same window (C6 / M-cust + M-dispatch guard)", () => {
    const { result } = renderHook(() => useTimeZone());
    expect(result.current[0]).toBe("local");

    act(() => {
      setTimeZone("utc");
    });

    expect(result.current[0]).toBe("utc");
  });

  it("re-renders subscribers when a foreign tab dispatches a 'storage' event for kafkito.timezone (C7 / M-storage guard)", () => {
    const { result } = renderHook(() => useTimeZone());
    expect(result.current[0]).toBe("local");

    act(() => {
      window.localStorage.setItem(tzKey, "utc");
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: tzKey,
          oldValue: null,
          newValue: "utc",
        }),
      );
    });

    expect(result.current[0]).toBe("utc");
  });

  it("re-derives the mode from localStorage rather than trusting the StorageEvent payload (C7b / M-source mutation-guard)", () => {
    const { result } = renderHook(() => useTimeZone());
    expect(result.current[0]).toBe("local");

    // Simulate a malformed/spoofed storage event whose newValue lies about
    // what is actually persisted. The handler must trust localStorage (the
    // source of truth) and resolve to "local", not blindly adopt the payload.
    act(() => {
      window.localStorage.setItem(tzKey, "");
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: tzKey,
          oldValue: null,
          newValue: "utc",
        }),
      );
    });

    expect(result.current[0]).toBe("local");
  });

  it("ignores 'storage' events for unrelated keys even when localStorage has drifted (C8 / M-filter mutation-guard)", () => {
    window.localStorage.setItem(tzKey, "utc");
    const { result } = renderHook(() => useTimeZone());
    expect(result.current[0]).toBe("utc");

    // Foreign tab clears the timezone (which would normally flip the hook
    // to "local"), but the storage event fires for a DIFFERENT key. The
    // filter must skip and the subscriber must keep its previous "utc".
    window.localStorage.setItem(tzKey, "local");
    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "some.other.key",
          oldValue: null,
          newValue: "anything",
        }),
      );
    });

    expect(result.current[0]).toBe("utc");
  });
});
