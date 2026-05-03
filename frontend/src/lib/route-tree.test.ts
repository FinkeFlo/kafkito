/// <reference types="node" />
import { describe, expect, it } from "vitest";
import { readdirSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const ROUTES_DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "routes");

function listRouteFiles(): string[] {
  return readdirSync(ROUTES_DIR, { withFileTypes: true })
    .filter((e) => e.isFile() && e.name.endsWith(".tsx") && !e.name.startsWith("__"))
    .map((e) => e.name);
}

function parentLayoutFor(file: string): string | null {
  const stem = file.replace(/\.tsx$/, "");
  if (!stem.includes(".")) return null;
  const segments = stem.split(".");
  segments.pop();
  return segments.join(".") + ".tsx";
}

describe("route tree integrity (Q-005 guard)", () => {
  it("every nested route file has a parent layout file", () => {
    const orphans: string[] = [];
    for (const file of listRouteFiles()) {
      const parent = parentLayoutFor(file);
      if (!parent) continue;
      if (!existsSync(join(ROUTES_DIR, parent))) {
        orphans.push(`${file} -> missing parent ${parent}`);
      }
    }
    expect(orphans).toEqual([]);
  });
});
