import { describe, expect, it } from "vitest";
import { countChanges, type DiffOp, diffLines, toSegments } from "./text-diff";

/** The diff as `-line`, `+line` and ` line` strings, like a unified diff. */
function unified(ops: DiffOp[] | null): string[] {
  if (!ops) throw new Error("diff gave up");
  return ops.map(
    (op) => `${op.type === "delete" ? "-" : op.type === "insert" ? "+" : " "}${op.line}`,
  );
}

describe("diffLines", () => {
  it("reports equal texts as unchanged lines", () => {
    expect(unified(diffLines("a\nb", "a\nb"))).toEqual([" a", " b"]);
  });

  it("finds a changed line between unchanged ones", () => {
    expect(unified(diffLines("a\nb\nc", "a\nx\nc"))).toEqual([" a", "-b", "+x", " c"]);
  });

  it("finds insertions and deletions", () => {
    expect(unified(diffLines("a\nc", "a\nb\nc"))).toEqual([" a", "+b", " c"]);
    expect(unified(diffLines("a\nb\nc", "a\nc"))).toEqual([" a", "-b", " c"]);
    expect(unified(diffLines("", "a"))).toEqual(["-", "+a"]);
  });

  it("produces a minimal edit script", () => {
    const ops = diffLines("a\nb\nc\na\nb\nb\na", "c\nb\na\nb\na\nc");
    const { deleted, inserted } = countChanges(ops ?? []);
    // Myers' textbook example: edit distance 5.
    expect(deleted + inserted).toBe(5);
  });

  it("numbers lines on both sides", () => {
    const ops = diffLines("a\nb\nc", "a\nx\nc") ?? [];
    expect(ops.map((o) => [o.a, o.b])).toEqual([
      [1, 1],
      [2, undefined],
      [undefined, 2],
      [3, 3],
    ]);
  });

  it("rebuilds both texts from the edit script", () => {
    const a = Array.from({ length: 200 }, (_, i) => `line ${i % 17} ${i % 5}`).join("\n");
    const b = Array.from({ length: 180 }, (_, i) => `line ${i % 13} ${i % 5}`).join("\n");
    const ops = diffLines(a, b) ?? [];
    const sideA = ops.filter((o) => o.type !== "insert").map((o) => o.line);
    const sideB = ops.filter((o) => o.type !== "delete").map((o) => o.line);
    expect(sideA.join("\n")).toBe(a);
    expect(sideB.join("\n")).toBe(b);
  });

  it("gives up past the edit limit", () => {
    const a = Array.from({ length: 50 }, (_, i) => `a${i}`).join("\n");
    const b = Array.from({ length: 50 }, (_, i) => `b${i}`).join("\n");
    expect(diffLines(a, b, 10)).toBeNull();
    expect(diffLines(a, b, 100)).not.toBeNull();
  });
});

describe("toSegments", () => {
  it("collapses long unchanged runs but keeps context around changes", () => {
    const a = Array.from({ length: 20 }, (_, i) => `l${i}`);
    const b = [...a];
    b[10] = "changed";
    const segments = toSegments(diffLines(a.join("\n"), b.join("\n")) ?? [], 3);
    expect(segments.map((s) => [s.kind, s.ops.length])).toEqual([
      ["skip", 7],
      ["lines", 8],
      ["skip", 6],
    ]);
  });

  it("keeps a short unchanged run between changes visible", () => {
    const segments = toSegments(diffLines("a\nb\nc\nd", "x\nb\nc\ny") ?? [], 3);
    expect(segments).toHaveLength(1);
    expect(segments[0].kind).toBe("lines");
  });
});
