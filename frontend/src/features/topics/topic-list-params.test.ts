import { describe, expect, it } from "vitest";
import { parseTopicListSearch } from "./topic-list-params";

describe("parseTopicListSearch", () => {
  it("returns an empty object for a clean URL", () => {
    expect(parseTopicListSearch({})).toEqual({});
  });

  it("keeps valid values", () => {
    expect(
      parseTopicListSearch({
        q: "orders",
        internal: true,
        partitions: "gt10",
        retention: "unknown",
        sort: "size",
        dir: "desc",
      }),
    ).toEqual({
      q: "orders",
      internal: true,
      partitions: "gt10",
      retention: "unknown",
      sort: "size",
      dir: "desc",
    });
  });

  it("accepts internal as a string from a hand-edited URL", () => {
    expect(parseTopicListSearch({ internal: "true" })).toEqual({ internal: true });
    expect(parseTopicListSearch({ internal: "false" })).toEqual({});
  });

  it("drops unknown or default values so the URL stays short", () => {
    expect(
      parseTopicListSearch({
        q: "",
        internal: false,
        partitions: "any",
        retention: "forever",
        sort: "owner",
        dir: "sideways",
      }),
    ).toEqual({});
  });

  it("defaults the direction to ascending when only a sort column is given", () => {
    expect(parseTopicListSearch({ sort: "lag" })).toEqual({ sort: "lag", dir: "asc" });
  });

  it("ignores a direction without a sort column", () => {
    expect(parseTopicListSearch({ dir: "desc" })).toEqual({});
  });

  it("keeps numeric-looking search text as text", () => {
    expect(parseTopicListSearch({ q: 42 })).toEqual({ q: "42" });
  });
});
