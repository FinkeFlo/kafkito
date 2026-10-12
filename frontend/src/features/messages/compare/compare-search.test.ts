import { describe, expect, it } from "vitest";
import { DEFAULT_COMPARE_OPTIONS } from "@/lib/message-compare";
import { compareOptions, compareView, optionsSearch, parseCompareSearch } from "./compare-search";

describe("compare search params", () => {
  it("keeps only valid values", () => {
    expect(
      parseCompareSearch({
        a: " prod/orders/0/1 ",
        b: "",
        view: "fancy",
        anyArrayOrder: "yes",
        anyElementOrder: true,
        kafkitoHeaders: true,
        onlyDiff: true,
      }),
    ).toEqual({
      a: "prod/orders/0/1",
      anyElementOrder: true,
      kafkitoHeaders: true,
      onlyDiff: true,
    });
  });

  it("defaults to the semantic view and the default options", () => {
    const s = parseCompareSearch({});
    expect(compareView(s)).toBe("semantic");
    expect(compareOptions(s)).toEqual(DEFAULT_COMPARE_OPTIONS);
  });

  it("round-trips options through the URL", () => {
    const opts = { ignoreArrayOrder: true, ignoreChildOrder: false, ignoreKafkitoHeaders: false };
    const s = parseCompareSearch(JSON.parse(JSON.stringify(optionsSearch(opts, "exact"))));
    expect(compareOptions(s)).toEqual(opts);
    expect(compareView(s)).toBe("exact");
  });
});
