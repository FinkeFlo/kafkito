import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearLastCluster, getLastCluster, setLastCluster } from "./last-cluster";

describe("last-cluster", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns null when nothing is stored", () => {
    expect(getLastCluster()).toBeNull();
  });

  it("round-trips a cluster name through the public API", () => {
    setLastCluster("IAK");

    const got = getLastCluster();

    expect(got).toBe("IAK");
  });

  it("stores names with special characters verbatim (un-encoded)", () => {
    setLastCluster("PROD AsPIRe Integration");

    const got = getLastCluster();

    expect(got).toBe("PROD AsPIRe Integration");
  });

  it("clears the value", () => {
    setLastCluster("IAK");

    clearLastCluster();

    expect(getLastCluster()).toBeNull();
  });

  it("survives storage being unavailable (private mode, quota)", () => {
    vi.spyOn(localStorage, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });

    expect(() => setLastCluster("IAK")).not.toThrow();
  });
});
