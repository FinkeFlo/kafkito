import { describe, expect, it } from "vitest";
import { latestVersion } from "./schema-version";

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
