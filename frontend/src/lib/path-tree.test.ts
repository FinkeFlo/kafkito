import { describe, expect, it } from "vitest";
import { buildPathTree, type PathInfo, type PathType } from "./path-tree";

// production: see MAX_SAMPLE_VALUES in path-tree.ts
const sampleValuesCap = 5;
// production: see MAX_DEPTH in path-tree.ts
const maxDepth = 20;

describe("buildPathTree", () => {
  it("returns an empty tree for no samples", () => {
    expect(buildPathTree([]).size).toBe(0);
  });

  it.each<[string, Partial<PathInfo>]>([
    ["$.id", { type: "string", sampleValues: ["x"], distinctCount: 1, fromN: 1 }],
    ["$.n", { type: "number", sampleValues: [7] }],
    ["$.ok", { type: "boolean", sampleValues: [true] }],
  ])("indexes scalar field %s with type and value", (path, expected) => {
    const tree = buildPathTree([{ id: "x", n: 7, ok: true }]);

    expect(tree.get(path)).toMatchObject(expected);
  });

  it("normalizes array indices to [*]", () => {
    const tree = buildPathTree([{ prices: [{ x: 1 }, { x: 2 }, { x: 3 }] }]);

    expect(tree.has("$.prices[0].x")).toBe(false);
    const star = tree.get("$.prices[*].x");
    expect(star?.type).toBe("number");
    expect(star?.distinctCount).toBe(3);
    expect(star?.sampleValues).toEqual([1, 2, 3]);
  });

  it("unions paths across multiple samples and tracks fromN", () => {
    const tree = buildPathTree([
      { a: 1, b: "x" },
      { a: 2 },
      { a: 3, b: "y" },
    ]);

    expect(tree.get("$.a")?.fromN).toBe(3);
    expect(tree.get("$.b")?.fromN).toBe(2);
    expect(tree.get("$.b")?.distinctCount).toBe(2);
  });

  it(`caps sampleValues at ${sampleValuesCap} distinct entries`, () => {
    const samples = Array.from({ length: sampleValuesCap + 2 }, (_, i) => ({ k: i + 1 }));

    const tree = buildPathTree(samples);

    expect(tree.get("$.k")?.distinctCount).toBe(samples.length);
    expect(tree.get("$.k")?.sampleValues).toHaveLength(sampleValuesCap);
  });

  it(`caps depth at ${maxDepth} levels`, () => {
    let cur: Record<string, unknown> = { leaf: 1 };
    for (let i = 0; i < maxDepth + 10; i++) cur = { x: cur };

    const tree = buildPathTree([cur]);

    const longest = [...tree.keys()].reduce((a, b) => (a.length > b.length ? a : b));
    const depth = (longest.match(/\.x/g) || []).length;
    expect(depth).toBeLessThanOrEqual(maxDepth);
  });

  it.each<[string, PathType]>([
    ["$.a", "object"],
    ["$.list", "array"],
  ])("indexes node %s with type %s", (path, expectedType) => {
    const tree = buildPathTree([{ a: { b: 1 }, list: [1, 2] }]);

    expect(tree.get(path)?.type).toBe(expectedType);
  });

  it("treats null as its own type", () => {
    const tree = buildPathTree([{ x: null }]);

    expect(tree.get("$.x")?.type).toBe("null");
  });

  it("ignores non-object samples (scalars or arrays at root)", () => {
    expect(buildPathTree(["a", 1, true, null]).size).toBe(0);
  });
});
