import { afterEach, describe, expect, it, vi } from "vitest";
import { claimOncePerSession } from "./once-per-session";

describe("claimOncePerSession", () => {
  afterEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it("returns true only the first time per key", () => {
    expect(claimOncePerSession("a")).toBe(true);
    expect(claimOncePerSession("a")).toBe(false);
    expect(claimOncePerSession("b")).toBe(true);
  });

  it("remembers the claim in sessionStorage across reloads of the module state", () => {
    claimOncePerSession("c");
    expect(sessionStorage.getItem("kafkito.once.c")).toBe("1");
  });

  it("still fires only once when sessionStorage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(claimOncePerSession("d")).toBe(true);
    expect(claimOncePerSession("d")).toBe(false);
  });
});
