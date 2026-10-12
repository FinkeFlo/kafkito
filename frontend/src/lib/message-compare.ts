// Compares two Kafka records part by part (key, value, headers) for the
// message compare page. Everything here works on what the API already
// returned to the browser, so masking and RBAC apply exactly as in the
// message list; nothing is compared that the user could not see.
import { base64ToUtf8, type Message } from "./api";
import { canonicalJson } from "./canonical-json";
import { canonicalXml } from "./canonical-xml";
import { isJsonLikeEncoding } from "./format";

/** Values above this size are compared on their 64 KB preview only. */
export const MAX_COMPARE_VALUE_BYTES = 4 * 1024 * 1024;

export type PartFormat = "json" | "xml" | "text" | "binary";

export interface PartContent {
  /** `null`: the record has no key or value at all; `empty`: zero bytes. */
  kind: "null" | "empty" | "text" | "binary";
  /** The text as kafkito renders it, or the bytes as base64 for `binary`. */
  data: string;
  format: PartFormat;
  /** A data_masking rule changed the content; `data` is the masked rendering. */
  masked: boolean;
  /** Only the 64 KB preview is available, not the full value. */
  preview: boolean;
}

export interface HeaderContent {
  /** Display text; non-UTF-8 values as the API's `0x…` rendering. */
  text: string;
  /** Raw bytes of a non-UTF-8 value, base64. */
  b64?: string;
  masked: boolean;
}

export interface MessageContent {
  key: PartContent;
  value: PartContent;
  headers: Map<string, HeaderContent>;
}

export interface CompareOptions {
  /** JSON: arrays are multisets. */
  ignoreArrayOrder: boolean;
  /** XML: siblings are multisets. */
  ignoreChildOrder: boolean;
  /** Leave out the provenance headers kafkito's produce, replay and copy add. */
  ignoreKafkitoHeaders: boolean;
}

export const DEFAULT_COMPARE_OPTIONS: CompareOptions = {
  ignoreArrayOrder: false,
  ignoreChildOrder: false,
  ignoreKafkitoHeaders: true,
};

export type Verdict = "identical" | "equivalent" | "different" | "not_comparable";

export interface PartComparison {
  verdict: Verdict;
  /** Format both sides share, or null when they differ. */
  format: PartFormat | null;
  /** Texts for the exact diff view (hex dumps for binary content). */
  exact: { a: string; b: string };
  /** Canonical texts for the semantic diff view, when both sides parse. */
  semantic: { a: string; b: string } | null;
  /** Plain-language remarks: what was normalized, what limited the compare. */
  notes: string[];
}

export type HeaderStatus = "same" | "different" | "only_a" | "only_b";

export interface HeaderRow {
  key: string;
  a?: HeaderContent;
  b?: HeaderContent;
  status: HeaderStatus;
}

export interface HeadersComparison {
  verdict: Verdict;
  rows: HeaderRow[];
  notes: string[];
}

export interface MessageComparison {
  verdict: Verdict;
  key: PartComparison;
  value: PartComparison;
  headers: HeadersComparison;
}

const KAFKITO_HEADER_RE = /^x-kafkito-/i;

export function isKafkitoHeader(key: string): boolean {
  return KAFKITO_HEADER_RE.test(key);
}

// --- building the content of one record --------------------------------------

function formatOf(encoding: string): PartFormat {
  if (isJsonLikeEncoding(encoding)) return "json";
  if (encoding === "xml") return "xml";
  return "text";
}

function part(
  encoding: string,
  text: string | undefined,
  b64: string | undefined,
  masked: boolean,
  preview: boolean,
): PartContent {
  // Checked before the null case: the API omits an empty key or value.
  if (encoding === "empty")
    return { kind: "empty", data: "", format: "text", masked, preview: false };
  if (encoding === "null")
    return { kind: "null", data: "", format: "text", masked, preview: false };
  // A masked binary value has no bytes left, only its rendering.
  if (b64 !== undefined && !masked) {
    return { kind: "binary", data: b64, format: "binary", masked, preview };
  }
  return { kind: "text", data: text ?? "", format: formatOf(encoding), masked, preview };
}

/**
 * The comparable content of a record. `fullValueB64` is the full value from
 * the raw endpoint (Schema Registry values decoded, like the list) for a
 * record whose list value was truncated; without it a truncated value is
 * compared on its preview.
 */
export function messageContent(m: Message, fullValueB64?: string): MessageContent {
  const truncated = m.value_truncated === true;
  let value: PartContent;
  if (truncated && fullValueB64 !== undefined) {
    value =
      m.value_encoding === "binary"
        ? { kind: "binary", data: fullValueB64, format: "binary", masked: false, preview: false }
        : {
            kind: "text",
            data: base64ToUtf8(fullValueB64),
            format: formatOf(m.value_encoding),
            masked: false,
            preview: false,
          };
  } else {
    value = part(m.value_encoding, m.value, m.value_b64, m.masked === true, truncated);
  }
  const key = part(m.key_encoding, m.key, m.key_b64, m.key_masked === true, false);
  const maskedHeaders = new Set(m.masked_headers ?? []);
  const headers = new Map<string, HeaderContent>();
  for (const [k, text] of Object.entries(m.headers ?? {})) {
    headers.set(k, { text, b64: m.headers_b64?.[k], masked: maskedHeaders.has(k) });
  }
  return { key, value, headers };
}

/** Whether the full value of `m` has to be fetched before comparing. */
export function needsFullValue(m: Message): boolean {
  return (
    m.value_truncated === true &&
    m.masked !== true &&
    (m.value_size_bytes ?? 0) <= MAX_COMPARE_VALUE_BYTES
  );
}

// --- canonical forms ---------------------------------------------------------

/**
 * JSON and XML above this many characters are compared exactly only: their
 * canonical forms take about a second per megabyte and side, on the main
 * thread.
 */
const MAX_SEMANTIC_CHARS = 1024 * 1024;

/** Whether `p` is JSON or XML too large for the semantic level. */
export function tooLargeForSemantic(p: PartContent): boolean {
  return (
    p.kind === "text" &&
    (p.format === "json" || p.format === "xml") &&
    p.data.length > MAX_SEMANTIC_CHARS
  );
}

/**
 * Canonical text of a part for the semantic level, or null when it is not
 * JSON or XML, does not parse, or is too large (tooLargeForSemantic).
 */
export function canonicalPart(p: PartContent, opts: CompareOptions): string | null {
  if (p.kind !== "text" || tooLargeForSemantic(p)) return null;
  if (p.format === "json")
    return canonicalJson(p.data, { ignoreArrayOrder: opts.ignoreArrayOrder });
  if (p.format === "xml") return canonicalXml(p.data, { ignoreChildOrder: opts.ignoreChildOrder });
  return null;
}

/** Display text of a part for the exact diff view. */
function exactText(p: PartContent): string {
  if (p.kind === "null") return "(null)";
  if (p.kind === "empty") return "(empty)";
  if (p.kind === "binary") return hexDump(p.data);
  return p.data;
}

const HEX_DUMP_MAX_BYTES = 64 * 1024;

/**
 * Classic 16-bytes-per-line hex dump, so binary values diff line by line.
 * Stops after 64 KB with a closing line that says so.
 */
export function hexDump(b64: string): string {
  const bin = atob(b64);
  const n = Math.min(bin.length, HEX_DUMP_MAX_BYTES);
  const lines: string[] = [];
  for (let i = 0; i < n; i += 16) {
    let hex = "";
    let ascii = "";
    for (let j = i; j < i + 16; j++) {
      if (j < n) {
        const c = bin.charCodeAt(j);
        hex += `${c.toString(16).padStart(2, "0")} `;
        ascii += c >= 0x20 && c < 0x7f ? bin[j] : ".";
      } else {
        hex += "   ";
      }
    }
    lines.push(`${i.toString(16).padStart(8, "0")}  ${hex} |${ascii}|`);
  }
  if (bin.length > n) lines.push(`… ${bin.length - n} more bytes`);
  return lines.join("\n");
}

// --- comparing ---------------------------------------------------------------

const PART_LABEL = { key: "key", value: "value" } as const;

function sameRaw(a: PartContent, b: PartContent): boolean {
  return a.kind === b.kind && a.data === b.data;
}

function comparePart(
  a: PartContent,
  b: PartContent,
  opts: CompareOptions,
  which: "key" | "value",
): PartComparison {
  const label = PART_LABEL[which];
  const notes: string[] = [];
  const format = a.format === b.format ? a.format : null;
  const exact = { a: exactText(a), b: exactText(b) };

  // Canonical forms are the expensive part; each is computed at most once.
  // `unordered` is the order option that applies to this format.
  const structured =
    (format === "json" || format === "xml") && a.kind === "text" && b.kind === "text";
  const unordered = format === "json" ? opts.ignoreArrayOrder : opts.ignoreChildOrder;
  const tooLarge = structured && (tooLargeForSemantic(a) || tooLargeForSemantic(b));
  const canon = (p: PartContent, u: boolean) =>
    canonicalPart(p, { ...opts, ignoreArrayOrder: u, ignoreChildOrder: u });
  const canonA = structured && !tooLarge ? canon(a, unordered) : null;
  const canonB = structured && !tooLarge ? canon(b, unordered) : null;
  const semantic = canonA !== null && canonB !== null ? { a: canonA, b: canonB } : null;

  if (tooLarge) {
    notes.push(`The ${label} is larger than 1 MB; compared exactly, without the semantic level.`);
  } else if (structured && !semantic) {
    const side = canonA === null ? (canonB === null ? "Neither side's" : "Side A's") : "Side B's";
    notes.push(`${side} ${label} does not parse as ${format?.toUpperCase()}; compared exactly.`);
  }
  if (a.kind === "text" && b.kind === "text" && a.format !== b.format) {
    notes.push(`The ${label}s have different formats; compared exactly.`);
  }

  let verdict: Verdict;
  if (sameRaw(a, b)) verdict = "identical";
  else if (semantic && semantic.a === semantic.b) {
    verdict = "equivalent";
    // Name the order option only when it was needed.
    const orderMatters = unordered && canon(a, false) !== canon(b, false);
    notes.push(equivalenceNote(format, orderMatters));
  } else {
    verdict = "different";
    // Reordering keeps the canonical text's length, so only equal lengths
    // are worth the second canonical form.
    if (semantic && !unordered && semantic.a.length === semantic.b.length) {
      const looseA = canon(a, true);
      if (looseA !== null && looseA === canon(b, true)) notes.push(orderHint(format));
    }
  }

  if (a.masked !== b.masked) {
    notes.push(`Only side ${a.masked ? "A" : "B"}'s ${label} is masked by data_masking rules.`);
    if (verdict === "different") verdict = "not_comparable";
  } else if (a.masked && b.masked) {
    notes.push(`Both ${label}s are masked; their masked renderings were compared.`);
  }
  if (a.preview || b.preview) {
    const side = a.preview && b.preview ? "both sides" : a.preview ? "side A" : "side B";
    notes.push(`Only the 64 KB preview of ${side} was compared.`);
    // A preview is a prefix of the full value. Unless it contradicts the
    // other side, the values may still be equal beyond it.
    if (verdict !== "not_comparable")
      verdict = previewsAgree(a, b) ? "not_comparable" : "different";
  }
  return { verdict, format, exact, semantic, notes };
}

function knownBytes(p: PartContent): string {
  return p.kind === "binary" ? atob(p.data) : p.data;
}

function previewsAgree(a: PartContent, b: PartContent): boolean {
  if (a.kind !== b.kind) return false;
  const ka = knownBytes(a);
  const kb = knownBytes(b);
  return (!a.preview || kb.startsWith(ka)) && (!b.preview || ka.startsWith(kb));
}

// Names what the semantic level had to look past.
function equivalenceNote(format: PartFormat | null, orderMatters: boolean): string {
  if (format === "xml") {
    return orderMatters
      ? "Equivalent once element order, attribute order, namespace prefixes and whitespace are ignored."
      : "Equivalent once attribute order, namespace prefixes and whitespace are ignored.";
  }
  return orderMatters
    ? "Equivalent once array order, key order, whitespace and number notation are ignored."
    : "Equivalent once key order, whitespace and number notation are ignored.";
}

// Points at the order option that would make two different parts equivalent.
function orderHint(format: PartFormat | null): string {
  return format === "xml"
    ? "Only the order of elements differs; ignore XML element order to treat them as equivalent."
    : "Only the order of array elements differs; ignore JSON array order to treat them as equivalent.";
}

function sameHeader(a: HeaderContent, b: HeaderContent): boolean {
  if (a.b64 !== undefined || b.b64 !== undefined) return a.b64 === b.b64;
  return a.text === b.text;
}

function compareHeaders(
  a: Map<string, HeaderContent>,
  b: Map<string, HeaderContent>,
  opts: CompareOptions,
): HeadersComparison {
  const notes: string[] = [];
  const keys = [...new Set([...a.keys(), ...b.keys()])].sort();
  const ignored = opts.ignoreKafkitoHeaders ? keys.filter(isKafkitoHeader) : [];
  if (ignored.length > 0) {
    notes.push(
      `${ignored.length} kafkito provenance header${ignored.length === 1 ? "" : "s"} (X-Kafkito-*) ignored.`,
    );
  }
  const rows: HeaderRow[] = [];
  let differs = false;
  let maskedDiffers = false;
  for (const key of keys) {
    if (opts.ignoreKafkitoHeaders && isKafkitoHeader(key)) continue;
    const ha = a.get(key);
    const hb = b.get(key);
    let status: HeaderStatus;
    if (!hb) status = "only_a";
    else if (!ha) status = "only_b";
    else status = sameHeader(ha, hb) ? "same" : "different";
    rows.push({ key, a: ha, b: hb, status });
    if (status === "same") continue;
    if (status === "different" && ha?.masked !== hb?.masked) maskedDiffers = true;
    else differs = true;
  }
  if (rows.some((r) => r.a?.masked || r.b?.masked)) {
    notes.push("Masked header values were compared as displayed.");
  }
  const verdict: Verdict = differs ? "different" : maskedDiffers ? "not_comparable" : "identical";
  return { verdict, rows, notes };
}

const VERDICT_RANK: Record<Verdict, number> = {
  identical: 0,
  equivalent: 1,
  not_comparable: 2,
  different: 3,
};

export function worstVerdict(...verdicts: Verdict[]): Verdict {
  return verdicts.reduce((w, v) => (VERDICT_RANK[v] > VERDICT_RANK[w] ? v : w), "identical");
}

export function compareMessages(
  a: MessageContent,
  b: MessageContent,
  opts: CompareOptions,
): MessageComparison {
  const key = comparePart(a.key, b.key, opts, "key");
  const value = comparePart(a.value, b.value, opts, "value");
  const headers = compareHeaders(a.headers, b.headers, opts);
  return {
    verdict: worstVerdict(key.verdict, value.verdict, headers.verdict),
    key,
    value,
    headers,
  };
}
