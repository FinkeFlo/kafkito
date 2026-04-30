import { describe, expect, it } from "vitest";
import { buildPathTree } from "./path-tree";

describe("buildPathTree", () => {
  it("returns an empty tree for no samples", () => {
    expect(buildPathTree([]).size).toBe(0);
  });

  it("indexes scalar fields with type and value", () => {
    const tree = buildPathTree([{ id: "x", n: 7, ok: true }]);
    expect(tree.get("$.id")).toMatchObject({ type: "string", sampleValues: ["x"], distinctCount: 1, fromN: 1 });
    expect(tree.get("$.n")).toMatchObject({ type: "number", sampleValues: [7] });
    expect(tree.get("$.ok")).toMatchObject({ type: "boolean", sampleValues: [true] });
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

  it("caps sampleValues at 5 distinct entries", () => {
    const samples = [{ k: 1 }, { k: 2 }, { k: 3 }, { k: 4 }, { k: 5 }, { k: 6 }, { k: 7 }];
    const tree = buildPathTree(samples);
    expect(tree.get("$.k")?.distinctCount).toBe(7);
    expect(tree.get("$.k")?.sampleValues).toHaveLength(5);
  });

  it("caps depth at 20 levels", () => {
    let cur: Record<string, unknown> = { leaf: 1 };
    for (let i = 0; i < 30; i++) cur = { x: cur };
    const tree = buildPathTree([cur]);
    const longest = [...tree.keys()].reduce((a, b) => (a.length > b.length ? a : b));
    const depth = (longest.match(/\.x/g) || []).length;
    expect(depth).toBeLessThanOrEqual(20);
  });

  it("indexes object and array nodes with type", () => {
    const tree = buildPathTree([{ a: { b: 1 }, list: [1, 2] }]);
    expect(tree.get("$.a")?.type).toBe("object");
    expect(tree.get("$.list")?.type).toBe("array");
  });

  it("treats null as its own type", () => {
    const tree = buildPathTree([{ x: null }]);
    expect(tree.get("$.x")?.type).toBe("null");
  });

  it("ignores non-object samples (scalars or arrays at root)", () => {
    expect(buildPathTree(["a", 1, true, null]).size).toBe(0);
  });
});
