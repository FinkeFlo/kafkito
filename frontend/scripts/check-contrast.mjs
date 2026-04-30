#!/usr/bin/env node
// WCAG-AA contrast scanner for Direction-A `@theme` tokens.
//
// 1. Parse `frontend/src/index.css` and pull every `--color-*` declaration
//    (with one-level alias resolution) for both `:root` (light) and
//    `html.dark { ... }` (dark) modes.
// 2. Walk `frontend/src/**/*.{tsx,css}` and infer (foreground, background)
//    pairs from co-occurring Tailwind utilities on the same `className`
//    (or `class:`) string. Also synthesizes the global focus-ring pair
//    for interactive elements (see `index.css:99-102`).
// 3. Compute OKLCH -> sRGB -> WCAG relative luminance -> contrast ratio
//    for each pair in both modes. Apply small-text vs large-text vs
//    non-text rules.
// 4. Honour `frontend/scripts/contrast-allowlist.json` (optional).
// 5. Emit a stdout report and a side `contrast-report.html`. Exit 1 on
//    any non-allow-listed failure.
//
// No external dependencies. Node built-ins only.
//
// See: docs/DESIGN_GUIDELINES.md (token usage), docs/design-review-2026-04.md
// section E1 (the failing pairs this scanner is designed to catch).

import { readFileSync, writeFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const FRONTEND_ROOT = resolve(HERE, "..");
const SRC_ROOT = join(FRONTEND_ROOT, "src");
const INDEX_CSS = join(SRC_ROOT, "index.css");
const ALLOWLIST_FILE = join(HERE, "contrast-allowlist.json");
const REPORT_HTML = join(HERE, "contrast-report.html");

// ---------------------------------------------------------------------------
// 1. Token table extraction
// ---------------------------------------------------------------------------

/**
 * Parse `--color-*` declarations from the file's `@theme { ... }` block (light)
 * and `html.dark { ... }` block (dark). Resolves one level of `var(--color-...)`
 * aliases against the same mode's table.
 */
function parseTokens(cssText) {
  const blocks = {
    light: extractBlock(cssText, /@theme\s*\{/),
    dark: extractBlock(cssText, /html\.dark\s*\{/),
  };

  const warnings = [];
  const rawLight = collectDecls(blocks.light);
  const rawDark = collectDecls(blocks.dark);

  // Dark mode inherits light-mode values for tokens not redeclared (the
  // browser does the same: --color-focus is :root-only and stays that way
  // in dark mode unless overridden). We keep them separate so the report
  // can flag :root-only tokens that land on dark surfaces.
  const inheritedDark = new Map(rawLight);
  for (const [k, v] of rawDark) inheritedDark.set(k, v);

  const light = resolveAll(rawLight, rawLight, "light", warnings);
  const dark = resolveAll(inheritedDark, inheritedDark, "dark", warnings);

  return { light, dark, rawLight, rawDark: inheritedDark, warnings };
}

function extractBlock(cssText, openRe) {
  const m = cssText.match(openRe);
  if (!m) return "";
  let depth = 1;
  let i = m.index + m[0].length;
  const start = i;
  while (i < cssText.length && depth > 0) {
    const ch = cssText[i];
    if (ch === "{") depth++;
    else if (ch === "}") depth--;
    if (depth === 0) break;
    i++;
  }
  return cssText.slice(start, i);
}

function collectDecls(blockText) {
  const out = new Map();
  // --color-foo: <value>; (value can contain parens, so match up to ;)
  const re = /(--color-[a-z0-9-]+)\s*:\s*([^;]+);/g;
  for (const m of blockText.matchAll(re)) {
    out.set(m[1].trim(), m[2].trim());
  }
  return out;
}

function resolveAll(rawMap, lookupMap, mode, warnings) {
  const out = new Map();
  for (const [name, raw] of rawMap) {
    const resolved = resolveOne(name, raw, lookupMap, mode, warnings, new Set());
    if (resolved) out.set(name, resolved);
  }
  return out;
}

function resolveOne(name, raw, lookupMap, mode, warnings, seen) {
  if (seen.has(name)) {
    warnings.push(`token cycle while resolving ${name} (${mode})`);
    return null;
  }
  seen.add(name);

  const trimmed = raw.trim();
  const aliasMatch = trimmed.match(/^var\(\s*(--color-[a-z0-9-]+)\s*\)$/);
  if (aliasMatch) {
    const target = aliasMatch[1];
    const targetRaw = lookupMap.get(target);
    if (!targetRaw) {
      warnings.push(
        `${name} (${mode}) aliases ${target} which is not declared in this mode`
      );
      return null;
    }
    return resolveOne(target, targetRaw, lookupMap, mode, warnings, seen);
  }

  const parsed = parseOklch(trimmed);
  if (!parsed) {
    warnings.push(`${name} (${mode}) could not parse value: ${trimmed}`);
    return null;
  }
  return parsed;
}

/**
 * Parse `oklch(L C H)` or `oklch(L C H / a)`.
 */
function parseOklch(value) {
  const m = value.match(/^oklch\(\s*([^)]+)\)\s*$/);
  if (!m) return null;
  const inner = m[1];
  // Split first on `/` to separate alpha, then on whitespace.
  const slash = inner.split("/");
  const head = slash[0].trim().split(/\s+/);
  if (head.length < 3) return null;
  const L = parseChannel(head[0]);
  const C = parseChannel(head[1]);
  const H = parseHue(head[2]);
  let alpha = 1;
  if (slash.length === 2) alpha = parseChannel(slash[1].trim());
  if ([L, C, H, alpha].some((x) => x == null || Number.isNaN(x))) return null;
  return { L, C, H, alpha };
}

function parseChannel(raw) {
  if (raw == null) return null;
  const s = String(raw).trim();
  if (s.endsWith("%")) return parseFloat(s) / 100;
  const n = parseFloat(s);
  if (Number.isNaN(n)) return null;
  return n;
}

function parseHue(raw) {
  const s = String(raw).trim();
  if (s.endsWith("deg")) return parseFloat(s);
  return parseFloat(s);
}

// ---------------------------------------------------------------------------
// 2. OKLCH -> linear sRGB -> sRGB -> luminance
//    Constants: Bjorn Ottosson, https://bottosson.github.io/posts/oklab/
// ---------------------------------------------------------------------------

function oklchToOklab({ L, C, H }) {
  const hRad = (H * Math.PI) / 180;
  return { L, a: C * Math.cos(hRad), b: C * Math.sin(hRad) };
}

function oklabToLinearSrgb({ L, a, b }) {
  const l_ = L + 0.3963377774 * a + 0.2158037573 * b;
  const m_ = L - 0.1055613458 * a - 0.0638541728 * b;
  const s_ = L - 0.0894841775 * a - 1.291485548 * b;

  const l = l_ * l_ * l_;
  const m = m_ * m_ * m_;
  const s = s_ * s_ * s_;

  return {
    r: 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    g: -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    b: -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  };
}

function gammaEncode(x) {
  // Linear sRGB component -> sRGB component.
  const sign = x < 0 ? -1 : 1;
  const ax = Math.abs(x);
  const enc = ax <= 0.0031308 ? 12.92 * ax : 1.055 * Math.pow(ax, 1 / 2.4) - 0.055;
  return sign * enc;
}

function clamp01(x) {
  if (Number.isNaN(x)) return 0;
  return Math.min(1, Math.max(0, x));
}

/** OKLCH -> sRGB triple in [0,1] (gamma-encoded, clamped, alpha discarded). */
function oklchToSrgb(parsed) {
  const linear = oklabToLinearSrgb(oklchToOklab(parsed));
  return {
    r: clamp01(gammaEncode(linear.r)),
    g: clamp01(gammaEncode(linear.g)),
    b: clamp01(gammaEncode(linear.b)),
  };
}

function srgbToLinearChannel(c) {
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

/** WCAG relative luminance from sRGB triple in [0,1]. */
function relativeLuminance({ r, g, b }) {
  const R = srgbToLinearChannel(r);
  const G = srgbToLinearChannel(g);
  const B = srgbToLinearChannel(b);
  return 0.2126 * R + 0.7152 * G + 0.0722 * B;
}

function contrastRatio(yA, yB) {
  const L1 = Math.max(yA, yB);
  const L2 = Math.min(yA, yB);
  return (L1 + 0.05) / (L2 + 0.05);
}

/** Composite a (possibly transparent) OKLCH foreground over an opaque
 *  OKLCH background, in sRGB space. Returns the resulting sRGB triple.
 *  When alpha is 1 this is just the foreground. */
function compositeOver(fgParsed, bgParsed) {
  const fg = oklchToSrgb(fgParsed);
  const bg = oklchToSrgb(bgParsed);
  const a = fgParsed.alpha;
  if (a >= 1) return fg;
  return {
    r: a * fg.r + (1 - a) * bg.r,
    g: a * fg.g + (1 - a) * bg.g,
    b: a * fg.b + (1 - a) * bg.b,
  };
}

// ---------------------------------------------------------------------------
// 3. Source walk + pair discovery
// ---------------------------------------------------------------------------

const FG_TOKEN_TO_VAR = new Map([
  ["text-text", "--color-text"],
  ["text-muted", "--color-muted"],
  ["text-subtle-text", "--color-subtle-text"],
  ["text-text-muted", "--color-text-muted"],
  ["text-text-subtle", "--color-text-subtle"],
  ["text-accent", "--color-accent"],
  ["text-accent-foreground", "--color-accent-foreground"],
  ["text-text-on-accent", "--color-text-on-accent"],
  ["text-success", "--color-success"],
  ["text-warning", "--color-warning"],
  ["text-danger", "--color-danger"],
  ["text-info", "--color-info"],
  ["text-tint-green-fg", "--color-tint-green-fg"],
  ["text-tint-amber-fg", "--color-tint-amber-fg"],
  ["text-tint-red-fg", "--color-tint-red-fg"],
]);

const BG_TOKEN_TO_VAR = new Map([
  ["bg-bg", "--color-bg"],
  ["bg-panel", "--color-panel"],
  ["bg-subtle", "--color-subtle"],
  ["bg-hover", "--color-hover"],
  ["bg-accent", "--color-accent"],
  ["bg-accent-hover", "--color-accent-hover"],
  ["bg-accent-subtle", "--color-accent-subtle"],
  ["bg-success", "--color-success"],
  ["bg-warning", "--color-warning"],
  ["bg-danger", "--color-danger"],
  ["bg-info", "--color-info"],
  ["bg-info-subtle", "--color-info-subtle"],
  ["bg-success-subtle", "--color-success-subtle"],
  ["bg-warning-subtle", "--color-warning-subtle"],
  ["bg-danger-subtle", "--color-danger-subtle"],
  ["bg-tint-green-bg", "--color-tint-green-bg"],
  ["bg-tint-amber-bg", "--color-tint-amber-bg"],
  ["bg-tint-red-bg", "--color-tint-red-bg"],
  ["bg-surface", "--color-surface"],
  ["bg-surface-raised", "--color-surface-raised"],
  ["bg-surface-subtle", "--color-surface-subtle"],
  ["bg-surface-hover", "--color-surface-hover"],
  ["bg-overlay", "--color-overlay"],
]);

const BORDER_TOKEN_TO_VAR = new Map([
  ["border-border", "--color-border"],
  ["border-border-strong", "--color-border-strong"],
  ["border-accent", "--color-accent"],
  ["border-success", "--color-success"],
  ["border-warning", "--color-warning"],
  ["border-danger", "--color-danger"],
  ["border-tint-green-fg", "--color-tint-green-fg"],
  ["border-tint-amber-fg", "--color-tint-amber-fg"],
  ["border-tint-red-fg", "--color-tint-red-fg"],
  ["border-tint-green-bg", "--color-tint-green-bg"],
  ["border-tint-amber-bg", "--color-tint-amber-bg"],
  ["border-tint-red-bg", "--color-tint-red-bg"],
]);

// Raw `text-[var(--color-...)]` and `bg-[var(--color-...)]` arbitrary forms.
const RAW_FG_RE = /text-\[var\((--color-[a-z0-9-]+)\)\]/g;
const RAW_BG_RE = /bg-\[var\((--color-[a-z0-9-]+)\)\]/g;

const SMALL_TEXT_RE = /\btext-(?:xs|sm|\[(?:11|12)px\])\b/;
const LARGE_TEXT_RE = /\btext-(?:base|lg|xl|2xl|3xl|4xl|5xl|6xl|7xl|8xl|9xl)\b/;
const BOLD_RE = /\bfont-(?:bold|semibold|extrabold|black)\b/;
const FOCUS_HINT_RE = /(focus|focus-visible|ring|outline|border-border-strong|focus-within)/;

const INTERACTIVE_TAG_RE = /<(button|a|input|select|textarea)[\s>]/i;

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    if (entry === "node_modules" || entry === "dist" || entry === ".tanstack") continue;
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) walk(full, out);
    else if (/\.(tsx|ts|css)$/.test(entry)) out.push(full);
  }
  return out;
}

/**
 * Extract candidate "class strings" from a source file. We scan for
 * `className="..."`, `className={"..."}`, `className={\`...\`}`, `class="..."`,
 * and `clsx(...)` / `cn(...)` / `twMerge(...)` argument bodies. Mimics how
 * `check-palette.sh` works: regex over text, no JSX parser.
 */
function extractClassStrings(src) {
  const out = [];
  // `className="..."` or `class="..."` - single quoted form too.
  const reAttr = /(?:className|class)\s*=\s*("([^"\\]|\\.)*"|'([^'\\]|\\.)*')/g;
  for (const m of src.matchAll(reAttr)) {
    out.push({ text: stripQuotes(m[1]), index: m.index });
  }
  // `className={"..."}` / `className={\`...\`}` / template literals more generally.
  const reTpl = /(?:className|class)\s*=\s*\{([\s\S]*?)\}/g;
  for (const m of src.matchAll(reTpl)) {
    const body = m[1];
    for (const lit of harvestLiterals(body)) out.push({ text: lit, index: m.index });
  }
  // `clsx(...)`, `cn(...)`, `twMerge(...)` - same harvest.
  const reHelper = /\b(?:clsx|cn|twMerge|classNames)\s*\(([\s\S]*?)\)/g;
  for (const m of src.matchAll(reHelper)) {
    for (const lit of harvestLiterals(m[1])) out.push({ text: lit, index: m.index });
  }
  return out.map((o) => ({ text: o.text, line: lineOf(src, o.index) }));
}

function stripQuotes(s) {
  return s.slice(1, -1);
}

function harvestLiterals(body) {
  const out = [];
  const reStr = /"([^"\\]|\\.)*"|'([^'\\]|\\.)*'|`([^`\\]|\\.)*`/g;
  for (const m of body.matchAll(reStr)) {
    out.push(stripQuotes(m[0]));
  }
  return out;
}

function lineOf(src, index) {
  let line = 1;
  for (let i = 0; i < index && i < src.length; i++) if (src[i] === "\n") line++;
  return line;
}

/**
 * Given a class string, gather { fg, bg, border, sizeKind, focusHinted }.
 */
function tokensFromClassString(text) {
  const fg = new Set();
  const bg = new Set();
  const border = new Set();

  for (const tok of text.split(/[\s,]+/)) {
    // Strip Tailwind variants like `hover:`, `dark:`, `disabled:`, etc.
    const stripped = tok.replace(/^(?:[a-z-]+:)+/, "");
    if (FG_TOKEN_TO_VAR.has(stripped)) fg.add(FG_TOKEN_TO_VAR.get(stripped));
    if (BG_TOKEN_TO_VAR.has(stripped)) bg.add(BG_TOKEN_TO_VAR.get(stripped));
    if (BORDER_TOKEN_TO_VAR.has(stripped)) border.add(BORDER_TOKEN_TO_VAR.get(stripped));
  }

  for (const m of text.matchAll(RAW_FG_RE)) fg.add(m[1]);
  for (const m of text.matchAll(RAW_BG_RE)) bg.add(m[1]);

  let sizeKind;
  if (SMALL_TEXT_RE.test(text)) sizeKind = "small";
  else if (LARGE_TEXT_RE.test(text) && BOLD_RE.test(text)) sizeKind = "large-bold";
  else sizeKind = "default"; // assume small body text -> 4.5:1

  return {
    fg: [...fg],
    bg: [...bg],
    border: [...border],
    sizeKind,
    focusHinted: FOCUS_HINT_RE.test(text),
  };
}

function pairKind(sizeKind) {
  if (sizeKind === "large-bold") return { required: 3.0, label: "large bold text" };
  if (sizeKind === "non-text") return { required: 3.0, label: "non-text contrast" };
  return { required: 4.5, label: "small body text" };
}

// ---------------------------------------------------------------------------
// 4. Allow-list
// ---------------------------------------------------------------------------

function loadAllowlist() {
  if (!existsSync(ALLOWLIST_FILE)) return [];
  try {
    const raw = readFileSync(ALLOWLIST_FILE, "utf8");
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      console.warn(`check-contrast: ${ALLOWLIST_FILE} is not a JSON array; ignoring.`);
      return [];
    }
    return parsed;
  } catch (err) {
    console.warn(`check-contrast: failed to read allow-list: ${err.message}`);
    return [];
  }
}

function isAllowlisted(allowlist, fg, bg, mode) {
  return allowlist.some(
    (e) => e.fg === fg && e.bg === bg && (e.mode === "both" || e.mode === mode)
  );
}

// ---------------------------------------------------------------------------
// 5. Main
// ---------------------------------------------------------------------------

function main() {
  const cssText = readFileSync(INDEX_CSS, "utf8");
  const tokens = parseTokens(cssText);
  for (const w of tokens.warnings) console.warn(`check-contrast: ${w}`);

  const allowlist = loadAllowlist();
  const files = walk(SRC_ROOT);

  /** Map<key, { fg, bg, sizeKind, kind, firstFile, firstLine, occurrences }> */
  const pairs = new Map();

  function recordPair({ fg, bg, sizeKind, kind, file, line }) {
    const key = `${fg}|${bg}|${sizeKind}|${kind}`;
    let entry = pairs.get(key);
    if (!entry) {
      entry = {
        fg,
        bg,
        sizeKind,
        kind, // "text" | "focus-ring" | "border-affordance"
        firstFile: file,
        firstLine: line,
        occurrences: 0,
      };
      pairs.set(key, entry);
    }
    entry.occurrences++;
  }

  for (const file of files) {
    const src = readFileSync(file, "utf8");
    const strings = extractClassStrings(src);
    const interactiveFile = INTERACTIVE_TAG_RE.test(src);

    for (const { text, line } of strings) {
      const { fg, bg, border, sizeKind, focusHinted } = tokensFromClassString(text);

      // Text pairs.
      for (const f of fg) {
        for (const b of bg) {
          recordPair({ fg: f, bg: b, sizeKind, kind: "text", file, line });
        }
      }

      // Border affordance: `border-border-strong` / `border-tint-*` over the
      // closest declared bg in the same string (3:1 non-text rule). Skip
      // pure `border-border` (decoration) unless `focusHinted`.
      for (const br of border) {
        const isStrong = br !== "--color-border";
        if (!isStrong && !focusHinted) continue;
        for (const b of bg) {
          recordPair({
            fg: br,
            bg: b,
            sizeKind: "non-text",
            kind: "border-affordance",
            file,
            line,
          });
        }
      }

      // Synthesize the global focus-ring pair (`--color-accent` outline,
      // see `index.css:99-102`) for any class string on a likely-interactive
      // element. The accent-on-accent failure (E1#1) falls out naturally
      // because pairs are deduplicated by (fg, bg, sizeKind, kind).
      if (interactiveFile) {
        for (const b of bg) {
          recordPair({
            fg: "--color-accent",
            bg: b,
            sizeKind: "non-text",
            kind: "focus-ring",
            file,
            line,
          });
        }
      }
    }
  }

  // Always include the global focus-ring against the canvas surfaces, even
  // if no class string above happened to mention them - this catches the
  // dark-mode `--color-accent` outline on a `--color-accent` filled button
  // (E1#1) plus the standard panel/bg surfaces.
  for (const surface of ["--color-bg", "--color-panel", "--color-subtle", "--color-accent"]) {
    const key = `--color-accent|${surface}|non-text|focus-ring`;
    if (!pairs.has(key)) {
      pairs.set(key, {
        fg: "--color-accent",
        bg: surface,
        sizeKind: "non-text",
        kind: "focus-ring",
        firstFile: INDEX_CSS,
        firstLine: 99,
        occurrences: 0,
      });
    }
  }

  // ---- Evaluate every pair in both modes -------------------------------
  const rows = [];
  let failingNonAllowlisted = 0;

  for (const entry of pairs.values()) {
    for (const mode of ["light", "dark"]) {
      const table = mode === "light" ? tokens.light : tokens.dark;
      const fgParsed = table.get(entry.fg);
      const bgParsed = table.get(entry.bg);

      const { required } = pairKind(entry.sizeKind);
      const allowlisted = isAllowlisted(allowlist, entry.fg, entry.bg, mode);

      if (!fgParsed || !bgParsed) {
        const reason = !fgParsed
          ? `${entry.fg} undeclared in ${mode}`
          : `${entry.bg} undeclared in ${mode}`;
        const failed = !allowlisted;
        if (failed) failingNonAllowlisted++;
        rows.push({
          fg: entry.fg,
          bg: entry.bg,
          mode,
          required,
          measured: null,
          status: allowlisted ? "WARN-ALLOWLIST" : "FAIL-UNDECLARED",
          file: entry.firstFile,
          line: entry.firstLine,
          kind: entry.kind,
          sizeKind: entry.sizeKind,
          allowlisted,
          note: reason,
        });
        continue;
      }

      // Composite alpha against the background before measuring (matters for
      // --color-overlay, which carries / 0.45).
      const fgSrgb = compositeOver(fgParsed, bgParsed);
      const bgSrgb = oklchToSrgb(bgParsed);
      const ratio = contrastRatio(relativeLuminance(fgSrgb), relativeLuminance(bgSrgb));
      const passes = ratio + 1e-6 >= required;
      let status;
      if (passes) status = "PASS";
      else if (allowlisted) status = "WARN-ALLOWLIST";
      else {
        status = "FAIL";
        failingNonAllowlisted++;
      }

      rows.push({
        fg: entry.fg,
        bg: entry.bg,
        mode,
        required,
        measured: ratio,
        status,
        file: entry.firstFile,
        line: entry.firstLine,
        kind: entry.kind,
        sizeKind: entry.sizeKind,
        allowlisted,
        note: "",
      });
    }
  }

  // ---- Stdout report ---------------------------------------------------
  const byFile = new Map();
  for (const r of rows) {
    if (!byFile.has(r.file)) byFile.set(r.file, []);
    byFile.get(r.file).push(r);
  }

  const totalPairs = pairs.size;
  const totalRows = rows.length;
  const failing = rows.filter((r) => r.status === "FAIL" || r.status === "FAIL-UNDECLARED");

  console.log(`check-contrast: scanned ${files.length} files, ${totalPairs} unique pairs (${totalRows} pair-mode rows)`);
  console.log(`check-contrast: failing (non-allow-listed): ${failing.length}`);
  console.log("");

  const sortedFiles = [...byFile.keys()].sort();
  for (const f of sortedFiles) {
    const localFails = byFile.get(f).filter((r) => r.status !== "PASS");
    if (localFails.length === 0) continue;
    console.log(`  ${relative(FRONTEND_ROOT, f)}`);
    for (const r of localFails) {
      const measured = r.measured == null ? "n/a" : r.measured.toFixed(2);
      console.log(
        `    [${r.status}] ${r.fg} on ${r.bg} (${r.mode}, ${r.kind}/${r.sizeKind})` +
          `  required ${r.required.toFixed(1)}, measured ${measured}` +
          (r.note ? `  - ${r.note}` : "") +
          `  @${r.line}`
      );
    }
  }
  console.log("");

  if (tokens.warnings.length) {
    console.log("token warnings:");
    for (const w of tokens.warnings) console.log(`  ${w}`);
    console.log("");
  }

  // ---- HTML report -----------------------------------------------------
  writeFileSync(
    REPORT_HTML,
    renderHtml({
      rows,
      files: files.length,
      totalPairs,
      failing: failing.length,
      tokenWarnings: tokens.warnings,
    })
  );
  console.log(`check-contrast: report written to ${relative(FRONTEND_ROOT, REPORT_HTML)}`);

  process.exit(failing.length > 0 ? 1 : 0);
}

function renderHtml({ rows, files, totalPairs, failing, tokenWarnings }) {
  const esc = (s) =>
    String(s).replace(/[&<>"']/g, (c) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;",
    })[c]);
  const order = { FAIL: 0, "FAIL-UNDECLARED": 0, "WARN-ALLOWLIST": 1, PASS: 2 };
  const rowHtml = rows
    .slice()
    .sort((a, b) => (order[a.status] ?? 9) - (order[b.status] ?? 9))
    .map((r) => {
      const measured = r.measured == null ? "n/a" : r.measured.toFixed(2);
      const statusColor =
        r.status === "PASS"
          ? "#1f7a3a"
          : r.status === "WARN-ALLOWLIST"
          ? "#a05a00"
          : "#a02020";
      return `<tr>
        <td><code>${esc(r.fg)}</code></td>
        <td><code>${esc(r.bg)}</code></td>
        <td>${esc(r.mode)}</td>
        <td>${r.required.toFixed(1)}</td>
        <td>${esc(measured)}</td>
        <td style="color:${statusColor};font-weight:600;">${esc(r.status)}</td>
        <td><code>${esc(relative(FRONTEND_ROOT, r.file))}:${r.line}</code></td>
        <td>${esc(r.kind)} / ${esc(r.sizeKind)}</td>
        <td>${esc(r.note)}</td>
      </tr>`;
    })
    .join("\n");
  const warnHtml = tokenWarnings.length
    ? `<h2>Token warnings</h2><ul>${tokenWarnings
        .map((w) => `<li>${esc(w)}</li>`)
        .join("")}</ul>`
    : "";
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>kafkito contrast report</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 24px; color: #1a1a1a; background: #fafafa; }
  h1 { font-size: 18px; margin: 0 0 8px; }
  p.meta { color: #555; margin: 0 0 16px; font-size: 13px; }
  table { border-collapse: collapse; width: 100%; font-size: 12px; background: #fff; }
  th, td { border: 1px solid #e0e0e0; padding: 6px 8px; text-align: left; vertical-align: top; }
  th { background: #f0f0f0; font-weight: 600; }
  code { font-family: ui-monospace, "SF Mono", Menlo, monospace; font-size: 11px; }
  tr:nth-child(even) td { background: #fafafa; }
  h2 { font-size: 14px; margin: 24px 0 8px; }
  ul { margin: 0 0 16px 18px; padding: 0; font-size: 12px; }
</style>
</head>
<body>
<h1>kafkito Direction-A contrast report</h1>
<p class="meta">Files scanned: ${files} | Unique pairs: ${totalPairs} | Failing rows (non-allow-listed): ${failing}</p>
<table>
<thead>
<tr><th>fg</th><th>bg</th><th>mode</th><th>required</th><th>measured</th><th>status</th><th>first usage</th><th>kind / size</th><th>note</th></tr>
</thead>
<tbody>
${rowHtml}
</tbody>
</table>
${warnHtml}
</body>
</html>
`;
}

main();
