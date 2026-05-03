export type PathType =
  | "string"
  | "number"
  | "boolean"
  | "null"
  | "object"
  | "array";

export interface PathInfo {
  type: PathType;
  sampleValues: unknown[];
  distinctCount: number;
  fromN: number;
}

export type PathTree = Map<string, PathInfo>;

export const MAX_DEPTH = 20;
export const MAX_SAMPLE_VALUES = 5;

function detectType(v: unknown): PathType {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  if (typeof v === "object") return "object";
  if (typeof v === "string") return "string";
  if (typeof v === "number") return "number";
  if (typeof v === "boolean") return "boolean";
  return "object";
}

function sameScalar(a: unknown, b: unknown): boolean {
  if (typeof a !== typeof b) return false;
  return a === b;
}

function recordLeaf(
  tree: PathTree,
  seenInThisSample: Set<string>,
  path: string,
  value: unknown,
) {
  const type = detectType(value);
  const existing = tree.get(path);
  const firstTimeInSample = !seenInThisSample.has(path);
  seenInThisSample.add(path);

  if (!existing) {
    tree.set(path, {
      type,
      sampleValues: type === "object" || type === "array" ? [] : [value],
      distinctCount: type === "object" || type === "array" ? 0 : 1,
      fromN: 1,
    });
    return;
  }
  if (firstTimeInSample) {
    existing.fromN += 1;
  }
  if (type !== "object" && type !== "array") {
    const isDistinct = !existing.sampleValues.some((x) =>
      sameScalar(x, value),
    );
    if (isDistinct) {
      existing.distinctCount += 1;
      if (existing.sampleValues.length < MAX_SAMPLE_VALUES) {
        existing.sampleValues.push(value);
      }
    }
  }
}

function walk(
  tree: PathTree,
  seenInThisSample: Set<string>,
  node: unknown,
  path: string,
  depth: number,
) {
  if (depth > MAX_DEPTH) return;

  recordLeaf(tree, seenInThisSample, path, node);

  if (Array.isArray(node)) {
    for (const item of node) {
      walk(tree, seenInThisSample, item, `${path}[*]`, depth + 1);
    }
    return;
  }
  if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node as Record<string, unknown>)) {
      walk(tree, seenInThisSample, v, `${path}.${k}`, depth + 1);
    }
  }
}

export function buildPathTree(samples: unknown[]): PathTree {
  const tree: PathTree = new Map();
  for (const sample of samples) {
    if (!sample || typeof sample !== "object" || Array.isArray(sample)) continue;
    const seen = new Set<string>();
    for (const [k, v] of Object.entries(sample as Record<string, unknown>)) {
      walk(tree, seen, v, `$.${k}`, 1);
    }
  }
  return tree;
}
