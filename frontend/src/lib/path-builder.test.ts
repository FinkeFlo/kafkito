import { describe, expect, it } from "vitest";
import { buildJsonPath, wildcardArrayIndices, type Token } from "./path-builder";

describe("buildJsonPath", () => {
  it("returns '$' for an empty trail", () => {
    expect(buildJsonPath([])).toBe("$");
  });

  it("emits dot notation for plain keys", () => {
    const trail: Token[] = [
      { kind: "key", name: "order" },
      { kind: "key", name: "id" },
    ];
    expect(buildJsonPath(trail)).toBe("$.order.id");
  });

  it("emits [N] for index tokens", () => {
    const trail: Token[] = [
      { kind: "key", name: "prices" },
      { kind: "index", value: 0 },
      { kind: "key", name: "customerNumber" },
    ];
    expect(buildJsonPath(trail)).toBe("$.prices[0].customerNumber");
  });

  it("emits [*] for star tokens", () => {
    const trail: Token[] = [
      { kind: "key", name: "prices" },
      { kind: "star" },
      { kind: "key", name: "customerNumber" },
    ];
    expect(buildJsonPath(trail)).toBe("$.prices[*].customerNumber");
  });

  it("supports mixed array selectors at multiple depths", () => {
    const trail: Token[] = [
      { kind: "key", name: "a" },
      { kind: "star" },
      { kind: "key", name: "b" },
      { kind: "index", value: 2 },
      { kind: "key", name: "c" },
    ];
    expect(buildJsonPath(trail)).toBe("$.a[*].b[2].c");
  });

  it.each<[string, string]>([
    ["has space", "$['has space']"],
    ["weird-key", "$['weird-key']"],
    ["with.dot", "$['with.dot']"],
  ])("uses bracket notation for key %p (special characters)", (name, expected) => {
    expect(buildJsonPath([{ kind: "key", name }])).toBe(expected);
  });

  it("escapes single quotes in bracket-notation keys", () => {
    expect(
      buildJsonPath([{ kind: "key", name: "it's" }]),
    ).toBe("$['it\\'s']");
  });
});

// Regression coverage for the array-scope behaviour that replaced the
// ArrayScopePopover: picking a value inside an array always searches every
// entry, so these cases used to be covered by array-scope-popover.test.tsx.
describe("wildcardArrayIndices", () => {
  it("returns an empty trail unchanged", () => {
    expect(wildcardArrayIndices([])).toEqual([]);
  });

  it("leaves trails without any index untouched", () => {
    const trail: Token[] = [
      { kind: "key", name: "order" },
      { kind: "key", name: "id" },
    ];
    expect(wildcardArrayIndices(trail)).toEqual(trail);
    expect(buildJsonPath(wildcardArrayIndices(trail))).toBe("$.order.id");
  });

  it("replaces a single array index with a wildcard", () => {
    const trail: Token[] = [
      { kind: "key", name: "items" },
      { kind: "index", value: 0 },
      { kind: "key", name: "price" },
    ];
    expect(buildJsonPath(wildcardArrayIndices(trail))).toBe("$.items[*].price");
  });

  it("replaces every index in a nested array trail", () => {
    const trail: Token[] = [
      { kind: "key", name: "items" },
      { kind: "index", value: 0 },
      { kind: "key", name: "tags" },
      { kind: "index", value: 2 },
    ];
    expect(buildJsonPath(wildcardArrayIndices(trail))).toBe("$.items[*].tags[*]");
  });

  it("keeps existing wildcards as wildcards", () => {
    const trail: Token[] = [
      { kind: "key", name: "items" },
      { kind: "star" },
      { kind: "index", value: 7 },
    ];
    expect(buildJsonPath(wildcardArrayIndices(trail))).toBe("$.items[*][*]");
  });

  it("does not mutate the input trail", () => {
    const trail: Token[] = [
      { kind: "key", name: "items" },
      { kind: "index", value: 3 },
    ];
    wildcardArrayIndices(trail);
    expect(trail[1]).toEqual({ kind: "index", value: 3 });
  });

  it("wildcards a top-level array index", () => {
    const trail: Token[] = [
      { kind: "index", value: 5 },
      { kind: "key", name: "status" },
    ];
    expect(buildJsonPath(wildcardArrayIndices(trail))).toBe("$[*].status");
  });
});
