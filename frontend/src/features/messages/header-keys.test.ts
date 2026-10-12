import { describe, expect, it } from "vitest";
import { headerKeyStats } from "./header-keys";

describe("headerKeyStats", () => {
  it("counts the messages per header key, most frequent first, then by key", () => {
    expect(
      headerKeyStats([
        { headers: { "x-trace": "a", "content-type": "json" } },
        { headers: { "x-trace": "b", "x-tenant": "acme" } },
        { headers: { "x-trace": "c", "content-type": "json" } },
        {},
      ]),
    ).toEqual([
      { key: "x-trace", count: 3 },
      { key: "content-type", count: 2 },
      { key: "x-tenant", count: 1 },
    ]);
  });

  it("returns nothing for messages without headers", () => {
    expect(headerKeyStats([{}, { headers: {} }])).toEqual([]);
  });
});
