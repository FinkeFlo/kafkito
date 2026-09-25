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

/**
 * Upper bound on the number of distinct paths collected across all samples,
 * shared by the JSON and XML builders.
 *
 * Arrays collapse onto a single `[*]` path, so a large array cannot blow this
 * up — but an object keyed by id (`{"user_1": {…}, "user_2": {…}}`, a common
 * payload shape) yields one path per key. Since samples are hydrated up to
 * MAX_HYDRATE_VALUE_BYTES, a single message can now contribute tens of
 * thousands of paths, and PathSense builds a Fuse index over every one of
 * them and renders from it.
 *
 * Reaching the cap aborts the traversal rather than merely dropping the
 * surplus paths: both builders run synchronously inside a render-path memo,
 * and walking a hydrated 4 MB document to the end costs ~500 ms of blocked
 * main thread even when every path found is discarded. The trade-off is that
 * the paths already collected stop accumulating `fromN`/`distinctCount` from
 * the unvisited remainder. That only applies to documents that blow the cap,
 * whose suggestion list is arbitrarily truncated anyway; anything below the
 * cap is walked exactly as before.
 *
 * 2000 is chosen for the *input* side, not the output side — measured cost of
 * the Fuse index plus a keystroke is only ~0.5 ms at 2000 and still ~16 ms at
 * 50000, so the list stays responsive far beyond this. The bound that matters
 * is how much of a pathological document we agree to walk before giving up.
 */
export const MAX_PATHS = 2000;

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
    if (tree.size >= MAX_PATHS) return;
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
  // Stop descending once the cap is reached: every further node would only
  // build a path string and hit a full Map before being thrown away.
  if (tree.size >= MAX_PATHS) return;

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
    if (tree.size >= MAX_PATHS) break;
    if (!sample || typeof sample !== "object" || Array.isArray(sample)) continue;
    const seen = new Set<string>();
    for (const [k, v] of Object.entries(sample as Record<string, unknown>)) {
      walk(tree, seen, v, `$.${k}`, 1);
    }
  }
  return tree;
}
