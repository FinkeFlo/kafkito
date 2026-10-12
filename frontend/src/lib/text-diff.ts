// Line diff for the message compare page (Myers' O(ND) algorithm). Written
// here rather than pulled in as a dependency (DESIGN_GUIDELINES § 1); the
// compare page only needs lines, not words or characters.

export type DiffOpType = "equal" | "delete" | "insert";

export interface DiffOp {
  type: DiffOpType;
  line: string;
  /** 1-based line number on side A; absent for inserted lines. */
  a?: number;
  /** 1-based line number on side B; absent for deleted lines. */
  b?: number;
}

/**
 * Upper bound on the edit distance the diff explores. Past it the result is
 * a wall of changes nobody reads, and Myers' trace grows with the square of
 * the distance, so diffLines gives up instead.
 */
export const MAX_DIFF_EDITS = 2000;

function splitLines(text: string): string[] {
  return text.split("\n");
}

/**
 * Diffs two texts line by line. Returns null when the texts differ in more
 * than `maxEdits` inserted plus deleted lines.
 */
export function diffLines(a: string, b: string, maxEdits = MAX_DIFF_EDITS): DiffOp[] | null {
  const al = splitLines(a);
  const bl = splitLines(b);

  // Common prefix and suffix are cheap to peel off and usually most of a
  // message, which keeps the O(ND) part small.
  let start = 0;
  while (start < al.length && start < bl.length && al[start] === bl[start]) start++;
  let endA = al.length;
  let endB = bl.length;
  while (endA > start && endB > start && al[endA - 1] === bl[endB - 1]) {
    endA--;
    endB--;
  }

  const middle = myers(al.slice(start, endA), bl.slice(start, endB), maxEdits);
  if (!middle) return null;

  const ops: DiffOp[] = [];
  for (let i = 0; i < start; i++) ops.push({ type: "equal", line: al[i], a: i + 1, b: i + 1 });
  for (const op of middle) {
    ops.push({
      type: op.type,
      line: op.line,
      a: op.a === undefined ? undefined : op.a + start,
      b: op.b === undefined ? undefined : op.b + start,
    });
  }
  for (let i = endA, j = endB; i < al.length; i++, j++) {
    ops.push({ type: "equal", line: al[i], a: i + 1, b: j + 1 });
  }
  return ops;
}

// Myers' greedy algorithm with a per-step snapshot of the frontier for the
// backtrack. Line numbers in the result are 1-based within the slices.
function myers(a: string[], b: string[], maxEdits: number): DiffOp[] | null {
  const n = a.length;
  const m = b.length;
  if (n === 0 && m === 0) return [];

  const max = Math.min(n + m, maxEdits);
  const offset = max + 1;
  const v = new Int32Array(2 * max + 3);
  const trace: Int32Array[] = [];
  let found = -1;

  for (let d = 0; d <= max; d++) {
    // Snapshot only the diagonals reachable at this step: -d..d.
    const snap = new Int32Array(2 * d + 3);
    for (let k = -d - 1; k <= d + 1; k++) snap[k + d + 1] = v[k + offset];
    trace.push(snap);
    for (let k = -d; k <= d; k += 2) {
      let x: number;
      if (k === -d || (k !== d && v[k - 1 + offset] < v[k + 1 + offset])) {
        x = v[k + 1 + offset];
      } else {
        x = v[k - 1 + offset] + 1;
      }
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v[k + offset] = x;
      if (x >= n && y >= m) {
        found = d;
        break;
      }
    }
    if (found >= 0) break;
  }
  if (found < 0) return null;

  // Backtrack from (n, m) through the snapshots.
  const ops: DiffOp[] = [];
  let x = n;
  let y = m;
  for (let d = found; d > 0; d--) {
    const snap = trace[d];
    const at = (k: number) => snap[k + d + 1];
    const k = x - y;
    const prevK = k === -d || (k !== d && at(k - 1) < at(k + 1)) ? k + 1 : k - 1;
    const prevX = at(prevK);
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      ops.push({ type: "equal", line: a[x - 1], a: x, b: y });
      x--;
      y--;
    }
    if (x === prevX) {
      ops.push({ type: "insert", line: b[y - 1], b: y });
    } else {
      ops.push({ type: "delete", line: a[x - 1], a: x });
    }
    x = prevX;
    y = prevY;
  }
  while (x > 0 && y > 0) {
    ops.push({ type: "equal", line: a[x - 1], a: x, b: y });
    x--;
    y--;
  }
  return ops.reverse();
}

export type DiffSegment =
  | { kind: "lines"; ops: DiffOp[] }
  /** A run of unchanged lines collapsed out of view; `ops` expands it. */
  | { kind: "skip"; ops: DiffOp[] };

/**
 * Groups a diff into visible hunks with `context` unchanged lines around
 * every change; longer unchanged runs become a "skip" segment.
 */
export function toSegments(ops: DiffOp[], context = 3): DiffSegment[] {
  const segments: DiffSegment[] = [];
  let i = 0;
  while (i < ops.length) {
    if (ops[i].type !== "equal") {
      const run: DiffOp[] = [];
      while (i < ops.length && ops[i].type !== "equal") run.push(ops[i++]);
      pushLines(segments, run);
      continue;
    }
    const run: DiffOp[] = [];
    while (i < ops.length && ops[i].type === "equal") run.push(ops[i++]);
    const isFirst = run[0] === ops[0];
    const isLast = i >= ops.length;
    const keepBefore = isFirst ? 0 : context;
    const keepAfter = isLast ? 0 : context;
    if (run.length <= keepBefore + keepAfter + 1) {
      pushLines(segments, run);
      continue;
    }
    if (keepBefore > 0) pushLines(segments, run.slice(0, keepBefore));
    segments.push({ kind: "skip", ops: run.slice(keepBefore, run.length - keepAfter) });
    if (keepAfter > 0) pushLines(segments, run.slice(run.length - keepAfter));
  }
  return segments;
}

function pushLines(segments: DiffSegment[], ops: DiffOp[]) {
  const last = segments[segments.length - 1];
  if (last?.kind === "lines") last.ops.push(...ops);
  else segments.push({ kind: "lines", ops: [...ops] });
}

/** Number of deleted and inserted lines in a diff. */
export function countChanges(ops: DiffOp[]): { deleted: number; inserted: number } {
  let deleted = 0;
  let inserted = 0;
  for (const op of ops) {
    if (op.type === "delete") deleted++;
    else if (op.type === "insert") inserted++;
  }
  return { deleted, inserted };
}
