import { describe, expect, it } from "vitest";
import {
  buildPathTree,
  MAX_DEPTH,
  MAX_PATHS,
  MAX_SAMPLE_VALUES,
  type PathInfo,
  type PathType,
} from "./path-tree";

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

  it(`caps sampleValues at ${MAX_SAMPLE_VALUES} distinct entries`, () => {
    const samples = Array.from({ length: MAX_SAMPLE_VALUES + 2 }, (_, i) => ({ k: i + 1 }));

    const tree = buildPathTree(samples);

    expect(tree.get("$.k")?.distinctCount).toBe(samples.length);
    expect(tree.get("$.k")?.sampleValues).toHaveLength(MAX_SAMPLE_VALUES);
  });

  it(`caps depth at ${MAX_DEPTH} levels`, () => {
    let cur: Record<string, unknown> = { leaf: 1 };
    for (let i = 0; i < MAX_DEPTH + 10; i++) cur = { x: cur };

    const tree = buildPathTree([cur]);

    const longest = [...tree.keys()].reduce((a, b) => (a.length > b.length ? a : b));
    const depth = (longest.match(/\.x/g) || []).length;
    expect(depth).toBeLessThanOrEqual(MAX_DEPTH);
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

  it(`caps the tree at ${MAX_PATHS} paths`, () => {
    // An object keyed by id is the realistic trigger: unlike an array, whose
    // entries all collapse onto one `[*]` path, every key becomes its own
    // path. Hydrated samples reach several MB, so this is reachable in
    // practice rather than merely theoretical.
    const byId: Record<string, unknown> = {};
    for (let i = 0; i < MAX_PATHS + 50; i++) byId[`user_${i}`] = { name: "n" };

    const tree = buildPathTree([byId]);

    expect(tree.size).toBe(MAX_PATHS);
  });

  it("stops walking once the cap is reached instead of scanning the rest", () => {
    // The cap used to only reject surplus paths, so a hydrated multi-megabyte
    // sample was still walked to the end — building a path string and probing
    // a full Map for every one of tens of thousands of nodes, synchronously,
    // inside a render-path memo (~500 ms of blocked main thread).
    const byId: Record<string, unknown> = {};
    for (let i = 0; i < MAX_PATHS + 50; i++) byId[`user_${i}`] = { name: "n" };

    let touched = false;
    const tripwire = new Proxy(
      {},
      {
        ownKeys() {
          touched = true;
          return [];
        },
        get() {
          touched = true;
          return undefined;
        },
      },
    );

    const tree = buildPathTree([byId, tripwire]);

    expect(tree.size).toBe(MAX_PATHS);
    expect(touched).toBe(false);
  });

  it("walks every sample when the cap is not reached", () => {
    // Guards the early abort against over-reach: the common case must still
    // aggregate across all samples.
    const tree = buildPathTree([{ a: 1 }, { b: 2 }, { c: 3 }]);

    expect([...tree.keys()].sort()).toEqual(["$.a", "$.b", "$.c"]);
  });
});
