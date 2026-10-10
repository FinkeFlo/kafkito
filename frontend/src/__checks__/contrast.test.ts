/// <reference types="node" />
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// WCAG AA floor for the design-token pairs, including what the axe e2e walks
// can't see: non-text indicators (focus ring, strong border, status icons;
// 3:1) and status tints that only render on errors or warnings (4.5:1).
const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../index.css"), "utf8");
const block = (open: string) => css.slice(css.indexOf(open), css.indexOf("\n}", css.indexOf(open)));
const decls = (text: string) =>
  [...text.matchAll(/(--color-[a-z0-9-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()] as const);
const light = new Map(decls(block("@theme {")));
const modes = { light, dark: new Map([...light, ...decls(block("html.dark {"))]) };

function luminance(tokens: Map<string, string>, name: string): number {
  let value = tokens.get(`--color-${name}`) ?? "";
  while (value.startsWith("var(")) value = tokens.get(value.slice(4, -1)) ?? "";
  const [L, C, H] = (/oklch\(([\d.]+)\s+([\d.]+)\s+([\d.]+)/.exec(value) ?? [])
    .slice(1)
    .map(Number);
  const [a, b] = [C * Math.cos((H * Math.PI) / 180), C * Math.sin((H * Math.PI) / 180)];
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  const [r, g, bl] = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ].map((c) => Math.min(1, Math.max(0, c)));
  return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
}

const TEXT =
  "text/bg text/panel text/subtle muted/bg muted/panel muted/subtle subtle-text/panel accent/panel accent-foreground/accent accent-foreground/accent-hover success/tint-green-bg warning/tint-amber-bg danger/tint-red-bg accent/accent-subtle tint-green-fg/tint-green-bg tint-amber-fg/tint-amber-bg tint-red-fg/tint-red-bg danger/panel";
const NON_TEXT =
  "focus/bg focus/panel focus/subtle focus/hover focus-on-accent/accent focus-on-accent/danger border-strong/panel success/panel warning/panel danger/panel";

const PAIRS = [
  ...TEXT.split(" ").map((pair) => [pair, 4.5] as const),
  ...NON_TEXT.split(" ").map((pair) => [pair, 3] as const),
];

describe.each(Object.entries(modes))("design-token contrast (%s)", (_mode, tokens) => {
  it.each(PAIRS)("%s reaches %d:1", (pair, floor) => {
    const [y1, y2] = pair.split("/").map((name) => luminance(tokens, name));
    expect((Math.max(y1, y2) + 0.05) / (Math.min(y1, y2) + 0.05)).toBeGreaterThanOrEqual(floor);
  });
});
