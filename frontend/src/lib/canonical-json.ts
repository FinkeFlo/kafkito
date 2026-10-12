// Canonical form of a JSON document for the semantic message compare:
// object keys sorted, numbers in one notation, insignificant whitespace
// gone, optionally array elements sorted. Two documents are equivalent when
// their canonical forms are equal.
//
// The parser is our own instead of JSON.parse because JSON.parse turns every
// number into a double: 12345678901234567890 and 12345678901234567891 would
// compare equal. Here numbers keep their digits and are compared exactly.

export interface JsonCanonicalOptions {
  /** Treat arrays as multisets: [1, 2] is equivalent to [2, 1]. */
  ignoreArrayOrder?: boolean;
}

type JsonNode =
  | { t: "lit"; v: "null" | "true" | "false" }
  | { t: "num"; v: string }
  | { t: "str"; v: string }
  | { t: "arr"; items: JsonNode[] }
  | { t: "obj"; entries: Map<string, JsonNode> };

/** Nesting deeper than this is refused rather than risking the call stack. */
const MAX_NESTING = 512;

class JsonSyntaxError extends Error {}

/**
 * Pretty-printed canonical form of `text`, or null when it is not a single
 * well-formed JSON value.
 */
export function canonicalJson(text: string, opts: JsonCanonicalOptions = {}): string | null {
  let node: JsonNode;
  try {
    node = new Parser(text).document();
  } catch (e) {
    if (e instanceof JsonSyntaxError || e instanceof RangeError) return null;
    throw e;
  }
  return render(normalize(node, opts).node, "");
}

// --- parser ---------------------------------------------------------------

const NUMBER_RE = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;

class Parser {
  private pos = 0;

  constructor(private readonly src: string) {}

  document(): JsonNode {
    const node = this.value(0);
    this.ws();
    if (this.pos !== this.src.length) this.fail();
    return node;
  }

  private fail(): never {
    throw new JsonSyntaxError(`unexpected input at ${this.pos}`);
  }

  private ws() {
    while (this.pos < this.src.length) {
      const c = this.src.charCodeAt(this.pos);
      if (c === 0x20 || c === 0x0a || c === 0x0d || c === 0x09) this.pos++;
      else break;
    }
  }

  private value(depth: number): JsonNode {
    if (depth > MAX_NESTING) this.fail();
    this.ws();
    const c = this.src[this.pos];
    if (c === "{") return this.object(depth);
    if (c === "[") return this.array(depth);
    if (c === '"') return { t: "str", v: this.string() };
    for (const lit of ["null", "true", "false"] as const) {
      if (this.src.startsWith(lit, this.pos)) {
        this.pos += lit.length;
        return { t: "lit", v: lit };
      }
    }
    NUMBER_RE.lastIndex = this.pos;
    const m = NUMBER_RE.exec(this.src);
    if (!m) this.fail();
    this.pos += m[0].length;
    return { t: "num", v: normalizeNumber(m[0]) };
  }

  private object(depth: number): JsonNode {
    this.pos++; // {
    // Later duplicates win, as with JSON.parse.
    const entries = new Map<string, JsonNode>();
    this.ws();
    if (this.src[this.pos] === "}") {
      this.pos++;
      return { t: "obj", entries };
    }
    for (;;) {
      this.ws();
      if (this.src[this.pos] !== '"') this.fail();
      const key = this.string();
      this.ws();
      if (this.src[this.pos] !== ":") this.fail();
      this.pos++;
      entries.set(key, this.value(depth + 1));
      this.ws();
      const c = this.src[this.pos++];
      if (c === "}") return { t: "obj", entries };
      if (c !== ",") this.fail();
    }
  }

  private array(depth: number): JsonNode {
    this.pos++; // [
    const items: JsonNode[] = [];
    this.ws();
    if (this.src[this.pos] === "]") {
      this.pos++;
      return { t: "arr", items };
    }
    for (;;) {
      items.push(this.value(depth + 1));
      this.ws();
      const c = this.src[this.pos++];
      if (c === "]") return { t: "arr", items };
      if (c !== ",") this.fail();
    }
  }

  // Validates the string token by hand, then lets JSON.parse resolve the
  // escapes: "é" and "é" are the same string.
  private string(): string {
    const start = this.pos;
    this.pos++; // opening quote
    for (;;) {
      if (this.pos >= this.src.length) this.fail();
      const c = this.src.charCodeAt(this.pos);
      if (c === 0x22) break;
      if (c < 0x20) this.fail();
      if (c === 0x5c) {
        const e = this.src[this.pos + 1];
        if (e === "u") {
          if (!/^[0-9a-fA-F]{4}$/.test(this.src.slice(this.pos + 2, this.pos + 6))) this.fail();
          this.pos += 6;
          continue;
        }
        if (e === undefined || !'"\\/bfnrt'.includes(e)) this.fail();
        this.pos += 2;
        continue;
      }
      this.pos++;
    }
    this.pos++; // closing quote
    return JSON.parse(this.src.slice(start, this.pos)) as string;
  }
}

/**
 * Exact decimal normal form of a JSON number literal: 1.0, 1e0 and 1 all
 * become "1", 0.10 becomes "0.1", -0 becomes "0". No floating point is
 * involved, so integers of any size keep every digit.
 */
export function normalizeNumber(literal: string): string {
  const m = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(literal);
  if (!m) return literal;
  const [, sign, int, frac = "", expPart = "0"] = m;
  let digits = (int + frac).replace(/^0+/, "");
  if (digits === "") return "0";
  let exp = Number(expPart) - frac.length;
  const trimmed = digits.replace(/0+$/, "");
  exp += digits.length - trimmed.length;
  digits = trimmed;
  const len = digits.length;
  let out: string;
  if (exp >= 0 && len + exp <= 21) {
    out = digits + "0".repeat(exp);
  } else if (exp < 0 && -exp < len) {
    out = `${digits.slice(0, len + exp)}.${digits.slice(len + exp)}`;
  } else if (exp < 0 && -exp - len < 6) {
    out = `0.${"0".repeat(-exp - len)}${digits}`;
  } else {
    const rest = digits.slice(1);
    out = `${digits[0]}${rest ? `.${rest}` : ""}e${exp + len - 1}`;
  }
  return sign + out;
}

// --- normalization and output -----------------------------------------------

interface Normalized {
  node: JsonNode;
  /** Compact canonical text of `node`, used to sort array elements. */
  key: string;
}

function normalize(node: JsonNode, opts: JsonCanonicalOptions): Normalized {
  switch (node.t) {
    case "lit":
    case "num":
      return { node, key: node.v };
    case "str":
      return { node, key: JSON.stringify(node.v) };
    case "arr": {
      const items = node.items.map((item) => normalize(item, opts));
      if (opts.ignoreArrayOrder) items.sort((x, y) => compareKeys(x.key, y.key));
      return {
        node: { t: "arr", items: items.map((i) => i.node) },
        key: `[${items.map((i) => i.key).join(",")}]`,
      };
    }
    case "obj": {
      const names = [...node.entries.keys()].sort(compareKeys);
      const entries = new Map<string, JsonNode>();
      const parts: string[] = [];
      for (const name of names) {
        const child = normalize(node.entries.get(name) as JsonNode, opts);
        entries.set(name, child.node);
        parts.push(`${JSON.stringify(name)}:${child.key}`);
      }
      return { node: { t: "obj", entries }, key: `{${parts.join(",")}}` };
    }
  }
}

// Code-unit order, independent of the locale.
function compareKeys(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function render(node: JsonNode, indent: string): string {
  switch (node.t) {
    case "lit":
    case "num":
      return node.v;
    case "str":
      return JSON.stringify(node.v);
    case "arr": {
      if (node.items.length === 0) return "[]";
      const inner = `${indent}  `;
      return `[\n${node.items.map((i) => inner + render(i, inner)).join(",\n")}\n${indent}]`;
    }
    case "obj": {
      if (node.entries.size === 0) return "{}";
      const inner = `${indent}  `;
      const lines = [...node.entries].map(
        ([k, v]) => `${inner}${JSON.stringify(k)}: ${render(v, inner)}`,
      );
      return `{\n${lines.join(",\n")}\n${indent}}`;
    }
  }
}
