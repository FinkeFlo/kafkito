import { describe, expect, it } from "vitest";
import { latestVersion, searchParamString } from "./schema-version";

describe("latestVersion", () => {
  it("returns the maximum regardless of order", () => {
    expect(latestVersion([1, 2, 3])).toBe(3);
    expect(latestVersion([3, 1, 2])).toBe(3);
    expect(latestVersion([5])).toBe(5);
  });

  it("defaults to 1 for an empty list", () => {
    expect(latestVersion([])).toBe(1);
  });
});

describe("searchParamString", () => {
  it("keeps strings", () => {
    expect(searchParamString("latest")).toBe("latest");
    expect(searchParamString("orders-value")).toBe("orders-value");
  });

  it("turns the numbers the router parses from `?version=1` back into strings", () => {
    expect(searchParamString(1)).toBe("1");
    expect(searchParamString(12)).toBe("12");
  });

  it("drops everything else", () => {
    expect(searchParamString(undefined)).toBeUndefined();
    expect(searchParamString(null)).toBeUndefined();
    expect(searchParamString(true)).toBeUndefined();
    expect(searchParamString(Number.NaN)).toBeUndefined();
    expect(searchParamString({ v: 1 })).toBeUndefined();
  });
});
