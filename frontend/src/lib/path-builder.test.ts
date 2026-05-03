import { describe, expect, it } from "vitest";
import { buildJsonPath, type Token } from "./path-builder";

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
