import { describe, it, expect, beforeEach } from "vitest";
import { getLastCluster, setLastCluster, clearLastCluster } from "./last-cluster";

const KEY = "kafkito.lastClusterId";

describe("last-cluster", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("returns null when nothing is stored", () => {
    expect(getLastCluster()).toBeNull();
  });

  it("stores and retrieves a cluster name", () => {
    setLastCluster("IAK");
    expect(getLastCluster()).toBe("IAK");
    expect(localStorage.getItem(KEY)).toBe("IAK");
  });

  it("stores names with special characters verbatim (un-encoded)", () => {
    setLastCluster("PROD AsPIRe Integration");
    expect(getLastCluster()).toBe("PROD AsPIRe Integration");
  });

  it("clears the value", () => {
    setLastCluster("IAK");
    clearLastCluster();
    expect(getLastCluster()).toBeNull();
  });

  it("survives storage being unavailable (private mode etc.)", () => {
    // Simulate by spying on localStorage.setItem to throw
    const orig = Storage.prototype.setItem;
    Storage.prototype.setItem = () => {
      throw new Error("QuotaExceededError");
    };
    expect(() => setLastCluster("IAK")).not.toThrow();
    Storage.prototype.setItem = orig;
  });
});
