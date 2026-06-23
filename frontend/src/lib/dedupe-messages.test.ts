import { describe, expect, it } from "vitest";
import { dedupeMessages } from "./dedupe-messages";

describe("dedupeMessages", () => {
  it("drops later duplicates with the same partition+offset, preserving order", () => {
    const input = [
      { partition: 0, offset: 1, v: "a" },
      { partition: 0, offset: 2, v: "b" },
      { partition: 0, offset: 1, v: "a-dup" },
      { partition: 1, offset: 1, v: "c" },
    ];

    expect(dedupeMessages(input)).toEqual([
      { partition: 0, offset: 1, v: "a" },
      { partition: 0, offset: 2, v: "b" },
      { partition: 1, offset: 1, v: "c" },
    ]);
  });

  it("returns an empty array unchanged", () => {
    expect(dedupeMessages([])).toEqual([]);
  });
});
