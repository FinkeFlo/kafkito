/// <reference types="node" />
import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// Dates must render through <Timestamp> (src/components/timestamp.tsx).
// Flags raw Date formatters in non-test .ts/.tsx files under src:
//   - `.toLocaleDateString(` / `.toLocaleTimeString(` anywhere
//   - `new Date(...).toLocaleString(` (bare `x.toLocaleString(` is usually
//     Number#toLocaleString and is allowed)
//   - `new Date(...).toString|toISOString|toDateString|toTimeString(`
// A line is allowed if it, or the line directly above it, carries the marker
// `// allow-raw-date: <reason>` (filename stamps, JSON exports, ...).

const SRC_DIR = join(dirname(fileURLToPath(import.meta.url)), "..");

const ALLOWED_FILES = new Set([
  "lib/format.ts",
  "components/timestamp.tsx",
  "components/relative-time.tsx",
]);

const RAW_DATE =
  /\.toLocale(Date|Time)String\(|new Date\([^)]*\)\.toLocaleString\(|new Date\([^)]*\)\.(toString|toISOString|toDateString|toTimeString)\(/;
const MARKER = "allow-raw-date:";

function sourceFiles(): string[] {
  return readdirSync(SRC_DIR, { recursive: true, encoding: "utf8" })
    .map((f) => f.split("\\").join("/"))
    .filter((f) => /\.(ts|tsx)$/.test(f) && !f.includes(".test.") && !ALLOWED_FILES.has(f));
}

describe("date formatting", () => {
  it("no raw Date formatters bypass <Timestamp>", () => {
    const hits: string[] = [];
    for (const file of sourceFiles()) {
      const lines = readFileSync(join(SRC_DIR, file), "utf8").split("\n");
      lines.forEach((line, i) => {
        if (!RAW_DATE.test(line)) return;
        if (line.includes(MARKER)) return;
        if (i > 0 && lines[i - 1].includes(MARKER)) return;
        hits.push(`src/${file}:${i + 1}: ${line.trim()}`);
      });
    }
    // Fix: use <Timestamp value={msOrIso} /> from "@/components/timestamp",
    // or whitelist the line with `// allow-raw-date: <reason>`.
    expect(hits).toEqual([]);
  });
});
