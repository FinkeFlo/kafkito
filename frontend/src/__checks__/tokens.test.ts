/// <reference types="node" />
import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// Every `var(--color-X)` reference in src must point to a `--color-X` token
// declared in src/index.css (either the @theme block or the html.dark block).
// Tailwind emits arbitrary values such as `bg-[var(...)]` verbatim, so a
// typo there silently renders nothing.

const SRC_DIR = join(dirname(fileURLToPath(import.meta.url)), "..");
const INDEX_CSS = join(SRC_DIR, "index.css");

function declaredTokens(): Set<string> {
  const declared = new Set<string>();
  for (const line of readFileSync(INDEX_CSS, "utf8").split("\n")) {
    const m = /^\s*(--color-[a-z0-9-]+)\s*:/.exec(line);
    if (m) declared.add(m[1]);
  }
  return declared;
}

function sourceFiles(): string[] {
  return readdirSync(SRC_DIR, { recursive: true, encoding: "utf8" })
    .filter((f) => /\.(ts|tsx|css)$/.test(f))
    .map((f) => join(SRC_DIR, f))
    .filter((f) => f !== INDEX_CSS);
}

describe("color tokens", () => {
  it("index.css declares --color-* tokens", () => {
    expect(declaredTokens().size).toBeGreaterThan(0);
  });

  it("every var(--color-*) reference is declared in src/index.css", () => {
    const declared = declaredTokens();
    const unknown: string[] = [];
    for (const file of sourceFiles()) {
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          for (const m of line.matchAll(/var\((--color-[a-z0-9-]+)/g)) {
            if (!declared.has(m[1])) unknown.push(`src/${file.slice(SRC_DIR.length + 1)}:${i + 1}: var(${m[1]})`);
          }
        });
    }
    expect(unknown).toEqual([]);
  });
});
