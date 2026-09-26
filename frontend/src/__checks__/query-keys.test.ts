/// <reference types="node" />
import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// Query keys are defined once, in the queryOptions() factories under
// src/lib/queries/. Everywhere else a `queryKey:` must come from a factory:
//   useQuery(topicQueries.list(cluster))
//   useQuery({ ...topicQueries.list(cluster), enabled })
//   qc.invalidateQueries({ queryKey: topicQueries.list(cluster).queryKey })
//   qc.invalidateQueries({ queryKey: messageKeys.topic(cluster, topic) })
// Flags, outside src/lib/queries/ (test files included):
//   - `queryKey:` whose value is an array or string literal;
//   - `queryKey:` whose value is neither `<factory>(…).queryKey` nor a key
//     prefix from a `…Keys` object;
//   - the `queryKey` shorthand property (its source can't be checked);
//   - literal arrays passed positionally to QueryClient key methods.

const SRC_DIR = join(dirname(fileURLToPath(import.meta.url)), "..");

const SKIP = [/^lib\/queries\//, /^__checks__\//, /^routeTree\.gen\.ts$/, /^lib\/api\.gen\.ts$/];

const FROM_FACTORY = /\.queryKey$/;
const FROM_KEYS = /^[A-Za-z_$][\w$]*Keys\.[\w$]+(\(.*\))?$/s;
const POSITIONAL =
  /\b(getQueryData|setQueryData|getQueryState|getQueriesData|setQueriesData|ensureQueryData|fetchQuery|prefetchQuery)\(\s*\[/g;

/** Reads the expression starting at `i` up to a top-level `,`, `}` or `)`. */
function readExpression(src: string, i: number): string {
  let depth = 0;
  let j = i;
  for (; j < src.length; j++) {
    const ch = src[j];
    if (ch === "(" || ch === "[" || ch === "{") depth++;
    else if (ch === ")" || ch === "]" || ch === "}") {
      if (depth === 0) break;
      depth--;
    } else if (ch === "," && depth === 0) break;
  }
  return src.slice(i, j).trim();
}

function lineOf(src: string, index: number): number {
  return src.slice(0, index).split("\n").length;
}

function isComment(src: string, index: number): boolean {
  const start = src.lastIndexOf("\n", index) + 1;
  const line = src.slice(start, index).trimStart();
  return line.startsWith("//") || line.startsWith("*") || line.startsWith("/*");
}

function findViolations(src: string): { line: number; reason: string }[] {
  const out: { line: number; reason: string }[] = [];
  for (const m of src.matchAll(/\bqueryKey\b(\s*)([:,}])/g)) {
    const at = m.index ?? 0;
    if (isComment(src, at)) continue;
    if (src[at - 1] === ".") continue;
    if (m[2] !== ":") {
      out.push({ line: lineOf(src, at), reason: "queryKey shorthand" });
      continue;
    }
    const value = readExpression(src, at + m[0].length);
    if (/^[[`'"]/.test(value)) {
      out.push({ line: lineOf(src, at), reason: `inline literal ${value.split("\n")[0]}` });
    } else if (!FROM_FACTORY.test(value) && !FROM_KEYS.test(value)) {
      out.push({ line: lineOf(src, at), reason: `not from a query factory: ${value}` });
    }
  }
  for (const m of src.matchAll(POSITIONAL)) {
    const at = m.index ?? 0;
    if (isComment(src, at)) continue;
    out.push({ line: lineOf(src, at), reason: `literal key passed to ${m[1]}` });
  }
  return out;
}

function sourceFiles(): string[] {
  return readdirSync(SRC_DIR, { recursive: true, encoding: "utf8" })
    .map((f) => f.split("\\").join("/"))
    .filter((f) => /\.(ts|tsx)$/.test(f) && !SKIP.some((re) => re.test(f)));
}

describe("query keys", () => {
  it("come from the factories in src/lib/queries", () => {
    const hits: string[] = [];
    for (const file of sourceFiles()) {
      const src = readFileSync(join(SRC_DIR, file), "utf8");
      for (const v of findViolations(src)) hits.push(`src/${file}:${v.line}: ${v.reason}`);
    }
    // Fix: add or reuse a queryOptions() factory in src/lib/queries/ and
    // spread it (`...topicQueries.list(cluster)`) or use its `.queryKey`.
    expect(hits).toEqual([]);
  });

  it("the scanner flags planted violations", () => {
    const planted = `
      useQuery({ queryKey: ["topics", cluster], queryFn: () => fetchTopics(cluster) });
      useQuery({
        queryKey:
          ["groups", cluster],
      });
      qc.invalidateQueries({ queryKey: ["acls", cluster] });
      qc.invalidateQueries({ queryKey: "info" });
      qc.invalidateQueries({ queryKey: someArray });
      useQuery({ queryKey, queryFn });
      qc.setQueryData(["topic", c, t], data);
    `;
    expect(findViolations(planted).map((v) => v.reason.split(" ")[0])).toEqual([
      "inline",
      "inline",
      "inline",
      "inline",
      "not",
      "queryKey",
      "literal",
    ]);
  });

  it("the scanner accepts factory keys", () => {
    const ok = `
      useQuery(topicQueries.list(cluster));
      useQuery({ ...topicQueries.detail(cluster, topic), enabled: !!cluster });
      qc.invalidateQueries({ queryKey: topicQueries.list(cluster!).queryKey });
      qc.invalidateQueries({
        queryKey: groupQueries.detail(cluster, detail.group_id).queryKey,
      });
      qc.invalidateQueries({ queryKey: messageKeys.topic(cluster, topic) });
      qc.invalidateQueries({ queryKey: authKeys.all });
      const k = q.queryKey;
      // queryKey: ["comment", "is", "ignored"]
    `;
    expect(findViolations(ok)).toEqual([]);
  });
});
