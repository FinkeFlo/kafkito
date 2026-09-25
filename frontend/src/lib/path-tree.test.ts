import { describe, expect, it } from "vitest";
import { buildPathTree, MAX_DEPTH, MAX_PATHS, type PathInfo, type PathType } from "./path-tree";

describe("buildPathTree", () => {
  it("returns an empty tree for no samples", () => {
    expect(buildPathTree([]).size).toBe(0);
  });

  it.each<[string, Partial<PathInfo>]>([
    ["$.id", { type: "string" }],
    ["$.n", { type: "number" }],
    ["$.ok", { type: "boolean" }],
  ])("indexes scalar field %s with its type", (path, expected) => {
    const tree = buildPathTree([{ id: "x", n: 7, ok: true }]);

    expect(tree.get(path)).toMatchObject(expected);
  });

  it("indexes field names only, carrying no sample data", () => {
    const tree = buildPathTree([{ id: "secret-value", nested: { n: 42 } }]);

    expect([...tree.keys()].sort()).toEqual(["$.id", "$.nested", "$.nested.n"]);
    // The dropdown lists paths, not data: nothing from the payload may be
    // retained, or a multi-megabyte field would be kept alive by the tree.
    for (const info of tree.values()) {
      expect(Object.keys(info)).toEqual(["type"]);
    }
  });

  it("normalizes array indices to [*]", () => {
    const tree = buildPathTree([{ prices: [{ x: 1 }, { x: 2 }, { x: 3 }] }]);

    expect(tree.has("$.prices[0].x")).toBe(false);
    expect(tree.get("$.prices[*].x")?.type).toBe("number");
  });

  it("unions paths across multiple samples", () => {
    const tree = buildPathTree([{ a: 1, b: "x" }, { a: 2 }, { a: 3, b: "y" }]);

    expect([...tree.keys()].sort()).toEqual(["$.a", "$.b"]);
    expect(tree.get("$.a")?.type).toBe("number");
    expect(tree.get("$.b")?.type).toBe("string");
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

  it("ignores scalar samples", () => {
    expect(buildPathTree(["a", 1, true, null]).size).toBe(0);
  });

  it("indexes a record whose whole value is an array of objects", () => {
    // A batch of rows per message is a normal Kafka payload shape. These
    // samples used to be skipped outright, leaving an empty tree that the
    // UI then reported as "sample isn't JSON".
    const tree = buildPathTree([
      [
        { RUNID: "abc", meta: { step: 1 } },
        { RUNID: "def", meta: { step: 2 } },
      ],
    ]);

    // `$[*].RUNID` is what the backend evaluates; `$.RUNID` matches nothing
    // on a root-level array.
    expect(tree.get("$[*].RUNID")?.type).toBe("string");
    expect(tree.get("$[*].meta.step")?.type).toBe("number");
    expect(tree.has("$.RUNID")).toBe(false);
  });

  it("collapses entries of a root array onto one path, like nested arrays", () => {
    const tree = buildPathTree([[{ a: 1 }, { a: 2 }, { a: 3 }]]);

    expect([...tree.keys()]).toEqual(["$[*]", "$[*].a"]);
  });

  it("indexes a root array of scalars", () => {
    const tree = buildPathTree([["x", "y"]]);

    expect(tree.get("$[*]")?.type).toBe("string");
  });

  it("aggregates root-array and root-object samples into one tree", () => {
    const tree = buildPathTree([[{ a: 1 }], { b: 2 }]);

    expect(tree.has("$[*].a")).toBe(true);
    expect(tree.has("$.b")).toBe(true);
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
