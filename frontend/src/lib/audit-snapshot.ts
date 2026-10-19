// Audit snapshot: one message rendered as a PNG for test evidence, with the
// capture time, who captured it, the cluster, topic, partition, offset,
// record time, key, fingerprint and the value. Drawn on a <canvas>, always in
// the light theme, so no screenshot library is needed. Only data the message
// row already shows goes into it: masked parts stay masked, a preview stays a
// preview.

import { formatTimestamp, type TimestampZone } from "./format";

export interface AuditSnapshotInput {
  capturedAt: Date;
  zone: TimestampZone;
  user: string | null;
  email: string | null;
  cluster: string;
  isProd: boolean;
  topic: string;
  partition: string;
  offset: string;
  timestampMs: number;
  key: string | null;
  fingerprint: string | null;
  notes: string[];
  valueLabel: string;
  value: string;
}

export interface AuditSnapshotModel {
  title: string;
  fields: Array<[label: string, value: string]>;
  valueLabel: string;
  body: string[];
  /** Lines of the value left out because the image would get too tall. */
  omittedLines: number;
}

/** Cap on value lines; beyond it the canvas would exceed browser limits. */
export const MAX_BODY_LINES = 1500;
/** Characters per line of the value and of long field values. */
export const WRAP_COLUMNS = 120;
const LABEL_COLUMNS = 14;

export function snapshotModel(input: AuditSnapshotInput): AuditSnapshotModel {
  const who =
    input.user && input.email && input.user !== input.email
      ? `${input.user} <${input.email}>`
      : input.user || input.email || "—";
  const fields: Array<[string, string]> = [
    ["Captured", formatTimestamp(input.capturedAt, input.zone)],
    ["Captured by", who],
    ["Cluster", input.isProd ? `${input.cluster}  [PROD]` : input.cluster],
    ["Topic", input.topic],
    ["Partition", input.partition],
    ["Offset", input.offset],
    ["Record time", formatTimestamp(input.timestampMs, input.zone)],
    ["Key", input.key ?? "(no key)"],
    ["Fingerprint", input.fingerprint ?? "—"],
  ];
  for (const note of input.notes) fields.push(["Note", note]);

  const all = wrapText(input.value, WRAP_COLUMNS);
  const body = all.slice(0, MAX_BODY_LINES);
  return {
    title: "kafkito · Audit snapshot",
    fields,
    valueLabel: input.valueLabel,
    body,
    omittedLines: all.length - body.length,
  };
}

/** Splits text into lines of at most `columns` characters, keeping blank lines. */
export function wrapText(text: string, columns: number): string[] {
  const out: string[] = [];
  for (const line of text.replace(/\r\n?/g, "\n").split("\n")) {
    const chars = Array.from(line.replace(/\t/g, "  "));
    if (chars.length === 0) {
      out.push("");
      continue;
    }
    for (let i = 0; i < chars.length; i += columns) out.push(chars.slice(i, i + columns).join(""));
  }
  return out;
}

/** `kafkito-audit-<cluster>-<topic>-p<partition>-o<offset>-<yyyymmddThhmmss>.png` */
export function snapshotFilename(
  cluster: string,
  topic: string,
  partition: number,
  offset: number,
  capturedAt: Date,
): string {
  const safe = (s: string) => s.replace(/[^A-Za-z0-9._-]+/g, "_");
  const p = (n: number) => String(n).padStart(2, "0");
  const d = capturedAt;
  const stamp = `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}T${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
  return `kafkito-audit-${safe(cluster)}-${safe(topic)}-p${partition}-o${offset}-${stamp}.png`;
}

interface Palette {
  bg: string;
  panel: string;
  border: string;
  text: string;
  muted: string;
  danger: string;
  font: string;
}

/**
 * The light theme's tokens, whatever theme is active: evidence is printed
 * and filed, so it should look the same for everyone. The dark class is
 * lifted only while the values are read; nothing is painted in between.
 */
function palette(): Palette {
  const root = document.documentElement;
  const dark = root.classList.contains("dark");
  if (dark) root.classList.remove("dark");
  try {
    const css = getComputedStyle(root);
    const v = (name: string) => css.getPropertyValue(name).trim();
    return {
      bg: v("--color-bg"),
      panel: v("--color-panel"),
      border: v("--color-border"),
      text: v("--color-text"),
      muted: v("--color-muted"),
      danger: v("--color-danger"),
      font: v("--font-mono") || "monospace",
    };
  } finally {
    if (dark) root.classList.add("dark");
  }
}

const FONT_PX = 13;
const LINE_PX = 19;
const PAD = 28;
/** Browsers refuse canvases much taller than this (device pixels). */
const MAX_CANVAS_PX = 30000;

/** Draws the model on a canvas in the light theme and returns it as PNG. */
export async function renderSnapshotPng(model: AuditSnapshotModel): Promise<Blob> {
  const c = palette();
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("This browser cannot draw the snapshot.");

  const font = `${FONT_PX}px ${c.font}`;
  // A web font that the page has not used yet would fall back silently.
  await Promise.all([
    document.fonts?.load(font),
    document.fonts?.load(`600 ${Math.round(FONT_PX * 1.25)}px ${c.font}`),
  ]).catch(() => undefined);
  ctx.font = font;
  const charW = ctx.measureText("M").width;
  const labelW = charW * LABEL_COLUMNS;

  const fieldLines = model.fields.map(([label, value]) => ({
    label,
    lines: wrapText(value, WRAP_COLUMNS - LABEL_COLUMNS),
  }));
  const fieldRows = fieldLines.reduce((n, f) => n + f.lines.length, 0);
  const bodyRows = model.body.length + (model.omittedLines > 0 ? 2 : 0);

  const width = Math.ceil(PAD * 2 + charW * WRAP_COLUMNS + 24);
  const height = Math.ceil(
    PAD + LINE_PX * 1.6 + 16 + fieldRows * LINE_PX + 24 + LINE_PX + 12 + bodyRows * LINE_PX + PAD,
  );
  const scale = Math.max(0.5, Math.min(2, MAX_CANVAS_PX / height));
  canvas.width = Math.ceil(width * scale);
  canvas.height = Math.ceil(height * scale);
  ctx.scale(scale, scale);
  ctx.textBaseline = "top";

  ctx.fillStyle = c.bg;
  ctx.fillRect(0, 0, width, height);
  ctx.fillStyle = c.panel;
  ctx.strokeStyle = c.border;
  ctx.lineWidth = 1;
  ctx.fillRect(8.5, 8.5, width - 17, height - 17);
  ctx.strokeRect(8.5, 8.5, width - 17, height - 17);

  let y = PAD;
  ctx.fillStyle = c.text;
  ctx.font = `600 ${Math.round(FONT_PX * 1.25)}px ${c.font}`;
  ctx.fillText(model.title, PAD, y);
  y += LINE_PX * 1.6;
  rule(ctx, c.border, y, width);
  y += 16;

  ctx.font = font;
  for (const f of fieldLines) {
    ctx.fillStyle = c.muted;
    ctx.fillText(f.label, PAD, y);
    ctx.fillStyle = f.label === "Cluster" && f.lines[0]?.endsWith("[PROD]") ? c.danger : c.text;
    for (const line of f.lines) {
      ctx.fillText(line, PAD + labelW, y);
      y += LINE_PX;
    }
  }
  y += 8;
  rule(ctx, c.border, y, width);
  y += 16;

  ctx.fillStyle = c.muted;
  ctx.fillText(model.valueLabel.toUpperCase(), PAD, y);
  y += LINE_PX + 12;
  ctx.fillStyle = c.text;
  for (const line of model.body) {
    ctx.fillText(line, PAD, y);
    y += LINE_PX;
  }
  if (model.omittedLines > 0) {
    y += LINE_PX;
    ctx.fillStyle = c.muted;
    ctx.fillText(
      `… ${model.omittedLines} more lines not shown. Use Copy value for the complete value.`,
      PAD,
      y,
    );
  }

  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (b) => (b ? resolve(b) : reject(new Error("The snapshot could not be encoded."))),
      "image/png",
    ),
  );
}

function rule(ctx: CanvasRenderingContext2D, color: string, y: number, width: number) {
  ctx.strokeStyle = color;
  ctx.beginPath();
  ctx.moveTo(PAD, y + 0.5);
  ctx.lineTo(width - PAD, y + 0.5);
  ctx.stroke();
}

export function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
