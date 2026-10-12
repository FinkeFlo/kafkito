import { useMemo, useState } from "react";
import { Button } from "@/components/ui/Button";
import { Notice } from "@/components/ui/Notice";
import { useFormatters } from "@/lib/use-formatters";
import { type DiffOp, diffLines, MAX_DIFF_EDITS, toSegments } from "@/lib/text-diff";

/** Unchanged lines revealed per click on a collapsed run. */
const REVEAL_STEP = 500;
/** Lines longer than this are cut around the change so the page stays responsive. */
const MAX_LINE_CHARS = 2000;
const CLIP_CONTEXT = 400;

interface Props {
  a: string;
  b: string;
  /** What is being diffed, for the accessible table name ("value"). */
  label: string;
  /** Unchanged lines kept around each change; 0 shows only the changes. */
  context?: number;
}

/**
 * Unified line diff of A against B: removed lines marked "−", added lines
 * "+", each with its line number on both sides. Unchanged runs collapse to
 * a button that expands them; changed line pairs underline the part that
 * differs.
 */
export function DiffView({ a, b, label, context = 3 }: Props) {
  const fmt = useFormatters();
  const ops = useMemo(() => diffLines(a, b), [a, b]);
  const segments = useMemo(() => (ops ? toSegments(ops, context) : []), [ops, context]);
  // Per collapsed run: how many of its lines are revealed.
  const [revealed, setRevealed] = useState<ReadonlyMap<number, number>>(new Map());

  if (!ops) {
    return (
      <Notice intent="warning" title="Too many differences to show line by line">
        More than {fmt.number(MAX_DIFF_EDITS)} lines differ. The verdict above still holds.
      </Notice>
    );
  }
  if (a === b) {
    return <p className="text-sm text-muted">No differences in this view.</p>;
  }

  const pairs = pairChanges(ops);
  return (
    <section
      aria-label={`Differences in the ${label}`}
      // biome-ignore lint/a11y/noNoninteractiveTabindex: the diff scrolls inside a capped height; without a tab stop keyboard users cannot scroll it (axe scrollable-region-focusable).
      tabIndex={0}
      className="max-h-[32rem] overflow-auto rounded-md border border-border bg-panel"
    >
      <table className="w-full border-collapse font-mono text-[11px] leading-relaxed">
        <caption className="sr-only">Differences in the {label}, message A to message B</caption>
        <thead className="sr-only">
          <tr>
            <th scope="col">Line in A</th>
            <th scope="col">Line in B</th>
            <th scope="col">Change</th>
            <th scope="col">Text</th>
          </tr>
        </thead>
        <tbody>
          {segments.map((seg, i) => {
            if (seg.kind === "lines") {
              return seg.ops.map((op) => (
                <DiffRow
                  key={`${op.type}-${op.a ?? ""}-${op.b ?? ""}`}
                  op={op}
                  pair={pairs.get(op)}
                />
              ));
            }
            // A collapsed run of unchanged lines, revealed REVEAL_STEP at a
            // time so a huge run cannot flood the page.
            const shown = revealed.get(i) ?? 0;
            const rest = seg.ops.length - shown;
            const step = Math.min(rest, REVEAL_STEP);
            return [
              ...seg.ops
                .slice(0, shown)
                .map((op) => <DiffRow key={`equal-${op.a ?? ""}-${op.b ?? ""}`} op={op} />),
              rest > 0 && (
                <tr key={`skip-${seg.ops[0].a}`} className="bg-subtle">
                  <td colSpan={4} className="px-1 py-0.5">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setRevealed(new Map(revealed).set(i, shown + step))}
                    >
                      {step === rest
                        ? `Show ${fmt.number(rest)} unchanged line${rest === 1 ? "" : "s"}`
                        : `Show ${fmt.number(step)} of ${fmt.number(rest)} unchanged lines`}
                    </Button>
                  </td>
                </tr>
              ),
            ];
          })}
        </tbody>
      </table>
    </section>
  );
}

const ROW_STYLE: Record<DiffOp["type"], string> = {
  equal: "",
  delete: "bg-tint-red-bg",
  insert: "bg-tint-green-bg",
};

const MARKER: Record<DiffOp["type"], { sign: string; word: string; className: string }> = {
  equal: { sign: "", word: "", className: "" },
  delete: { sign: "−", word: "Removed", className: "text-tint-red-fg" },
  insert: { sign: "+", word: "Added", className: "text-tint-green-fg" },
};

function DiffRow({ op, pair }: { op: DiffOp; pair?: DiffOp }) {
  const marker = MARKER[op.type];
  return (
    <tr className={ROW_STYLE[op.type]}>
      <td className="w-px select-none whitespace-nowrap px-2 text-right align-top tabular-nums text-muted">
        {op.a ?? ""}
      </td>
      <td className="w-px select-none whitespace-nowrap px-2 text-right align-top tabular-nums text-muted">
        {op.b ?? ""}
      </td>
      <td className={`w-px select-none px-1 align-top font-semibold ${marker.className}`}>
        <span aria-hidden="true">{marker.sign}</span>
        {marker.word && <span className="sr-only">{marker.word}</span>}
      </td>
      <td className="whitespace-pre-wrap break-all px-2 align-top text-text">
        <LineText line={op.line} other={pair?.line} />
      </td>
    </tr>
  );
}

/**
 * Pairs the i-th removed line of a change block with its i-th added line,
 * so each can underline where it differs from the other.
 */
function pairChanges(ops: DiffOp[]): Map<DiffOp, DiffOp> {
  const pairs = new Map<DiffOp, DiffOp>();
  let i = 0;
  while (i < ops.length) {
    if (ops[i].type !== "delete") {
      i++;
      continue;
    }
    const dels: DiffOp[] = [];
    while (i < ops.length && ops[i].type === "delete") dels.push(ops[i++]);
    const ins: DiffOp[] = [];
    while (i < ops.length && ops[i].type === "insert") ins.push(ops[i++]);
    for (let k = 0; k < Math.min(dels.length, ins.length); k++) {
      pairs.set(dels[k], ins[k]);
      pairs.set(ins[k], dels[k]);
    }
  }
  return pairs;
}

/** The line, with the stretch that differs from `other` underlined. */
function LineText({ line, other }: { line: string; other?: string }) {
  if (other === undefined) return <>{clip(line)}</>;
  let start = 0;
  const max = Math.min(line.length, other.length);
  while (start < max && line[start] === other[start]) start++;
  let end = 0;
  while (end < max - start && line[line.length - 1 - end] === other[other.length - 1 - end]) {
    end++;
  }
  const stop = line.length - end;
  if (stop <= start) return <>{clip(line)}</>;
  const from = Math.max(0, start - CLIP_CONTEXT);
  const to = Math.min(line.length, stop + CLIP_CONTEXT);
  const changed =
    stop - start > MAX_LINE_CHARS
      ? `${line.slice(start, start + MAX_LINE_CHARS)}… (${stop - start - MAX_LINE_CHARS} more characters)`
      : line.slice(start, stop);
  return (
    <>
      {from > 0 && <span className="text-muted">…</span>}
      {line.slice(from, start)}
      <span className="font-semibold underline decoration-2 underline-offset-2">{changed}</span>
      {line.slice(stop, to)}
      {to < line.length && <span className="text-muted">…</span>}
    </>
  );
}

// Long unpaired lines (minified JSON in the exact view) are cut to the
// first MAX_LINE_CHARS characters.
function clip(line: string): string {
  return line.length > MAX_LINE_CHARS
    ? `${line.slice(0, MAX_LINE_CHARS)}… (${line.length - MAX_LINE_CHARS} more characters)`
    : line;
}
