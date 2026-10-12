import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  getCompareSelection,
  sameMessage,
  setCompareSelection,
  subscribeCompareSelection,
  useCompareSelection,
} from "./compare-selection";

const REF = { cluster: "prod/eu", topic: "orders", partition: 2, offset: 42 };

afterEach(() => {
  setCompareSelection(null);
  vi.restoreAllMocks();
});

describe("compare selection", () => {
  it("keeps the reference in the tab's session storage", () => {
    setCompareSelection(REF);
    expect(getCompareSelection()).toBe("prod/eu/orders/2/42");
    expect(sessionStorage.getItem("kafkito.compareSelection")).toBe("prod/eu/orders/2/42");
    setCompareSelection(null);
    expect(getCompareSelection()).toBeNull();
    expect(sessionStorage.getItem("kafkito.compareSelection")).toBeNull();
  });

  it("notifies subscribers", () => {
    const cb = vi.fn();
    const unsubscribe = subscribeCompareSelection(cb);
    setCompareSelection(REF);
    expect(cb).toHaveBeenCalledTimes(1);
    unsubscribe();
    setCompareSelection(null);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("falls back to memory when session storage throws", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    setCompareSelection(REF);
    expect(getCompareSelection()).toBe("prod/eu/orders/2/42");
  });

  it("re-renders the hook with the parsed selection", () => {
    const { result } = renderHook(() => useCompareSelection());
    expect(result.current).toBeNull();
    act(() => setCompareSelection(REF));
    expect(result.current).toEqual(REF);
  });

  it("compares references", () => {
    expect(sameMessage(REF, { ...REF })).toBe(true);
    expect(sameMessage(REF, { ...REF, offset: 43 })).toBe(false);
    expect(sameMessage(null, REF)).toBe(false);
  });
});
