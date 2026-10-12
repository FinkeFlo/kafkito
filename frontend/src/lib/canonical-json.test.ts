import { describe, expect, it } from "vitest";
import { canonicalJson, normalizeNumber } from "./canonical-json";

const same = (a: string, b: string, opts = {}) => canonicalJson(a, opts) === canonicalJson(b, opts);

describe("canonicalJson", () => {
  it("ignores key order and whitespace", () => {
    expect(
      same('{"a":1,"b":{"x":true,"y":null}}', '{ "b": { "y": null, "x": true }, "a": 1 }'),
    ).toBe(true);
  });

  it("pretty-prints with sorted keys", () => {
    expect(canonicalJson('{"b":[1,{"d":2,"c":3}],"a":"x"}')).toBe(
      [
        "{",
        '  "a": "x",',
        '  "b": [',
        "    1,",
        "    {",
        '      "c": 3,',
        '      "d": 2',
        "    }",
        "  ]",
        "}",
      ].join("\n"),
    );
  });

  it("keeps array order unless told otherwise", () => {
    expect(same("[1,2,3]", "[3,2,1]")).toBe(false);
    expect(same("[1,2,3]", "[3,2,1]", { ignoreArrayOrder: true })).toBe(true);
    expect(same('[{"a":1},{"a":2}]', '[{"a":2},{"a":1}]', { ignoreArrayOrder: true })).toBe(true);
  });

  it("treats arrays as multisets, not sets", () => {
    expect(same("[1,1,2]", "[1,2,2]", { ignoreArrayOrder: true })).toBe(false);
  });

  it("sorts nested arrays too when array order is ignored", () => {
    expect(same('{"a":[[2,1],[4,3]]}', '{"a":[[3,4],[1,2]]}', { ignoreArrayOrder: true })).toBe(
      true,
    );
  });

  it("compares numbers by value without losing digits", () => {
    expect(same("1", "1.0")).toBe(true);
    expect(same("100", "1e2")).toBe(true);
    expect(same("0.1", "0.10")).toBe(true);
    expect(same("12345678901234567890", "12345678901234567891")).toBe(false);
    expect(same("0.1", "0.2")).toBe(false);
  });

  it("resolves string escapes", () => {
    expect(same('"\\u00e9"', '"é"')).toBe(true);
    expect(same('{"\\u0061":1}', '{"a":1}')).toBe(true);
  });

  it("lets later duplicate keys win, like JSON.parse", () => {
    expect(same('{"a":1,"a":2}', '{"a":2}')).toBe(true);
  });

  it("returns null for anything that is not one JSON value", () => {
    for (const bad of [
      "",
      "{",
      '{"a":1',
      "[1,]",
      "{'a':1}",
      "01",
      "1 2",
      '"\\x"',
      "NaN",
      '{"a":1}x',
    ]) {
      expect(canonicalJson(bad), bad).toBeNull();
    }
  });

  it("accepts scalars at the top level", () => {
    expect(canonicalJson(' "x" ')).toBe('"x"');
    expect(canonicalJson("true")).toBe("true");
    expect(canonicalJson("[]")).toBe("[]");
    expect(canonicalJson("{}")).toBe("{}");
  });

  it("refuses absurd nesting instead of overflowing the stack", () => {
    expect(canonicalJson("[".repeat(10_000) + "]".repeat(10_000))).toBeNull();
    expect(canonicalJson("[".repeat(100) + "]".repeat(100))).not.toBeNull();
  });
});

describe("normalizeNumber", () => {
  it.each([
    ["0", "0"],
    ["-0", "0"],
    ["0.000", "0"],
    ["1.0", "1"],
    ["-1.50", "-1.5"],
    ["1e2", "100"],
    ["1.5e3", "1500"],
    ["150e-2", "1.5"],
    ["0.000001", "0.000001"],
    ["1e-7", "1e-7"],
    ["1e30", "1e30"],
    ["123456789012345678901234567890", "1.2345678901234567890123456789e29"],
    ["12345678901234567890", "12345678901234567890"],
  ])("%s → %s", (input, expected) => {
    expect(normalizeNumber(input)).toBe(expected);
  });
});
