// SHA-256 fingerprint of a record's key, value and headers for the message
// compare page: `kfp1:<flags>:<64 hex digits>`. The flags say how the
// content was read, so two fingerprints only match when they were taken the
// same way:
//   x  exact: the content as kafkito shows it, byte for byte
//   s  semantic: JSON and XML in their canonical form (see message-compare)
//   a  with s: array order ignored
//   c  with s: XML element order ignored
//   h  kafkito provenance headers (X-Kafkito-*) left out
// A fingerprint covers what the user can see: a masked part is hashed in its
// masked rendering, never in the original.
import {
  type CompareOptions,
  canonicalPart,
  isKafkitoHeader,
  type MessageContent,
  type PartContent,
  tooLargeForSemantic,
} from "./message-compare";

export interface FingerprintOptions extends CompareOptions {
  semantic: boolean;
}

const PREFIX = "kfp1";
const FINGERPRINT_RE = /^kfp1:([xsach]+):([0-9a-f]{64})$/;

export function fingerprintFlags(o: FingerprintOptions): string {
  let flags = o.semantic ? "s" : "x";
  if (o.semantic && o.ignoreArrayOrder) flags += "a";
  if (o.semantic && o.ignoreChildOrder) flags += "c";
  if (o.ignoreKafkitoHeaders) flags += "h";
  return flags;
}

/** The options and digest of a pasted fingerprint, or null when it is not one. */
export function parseFingerprint(
  text: string,
): { options: FingerprintOptions; hex: string } | null {
  const m = FINGERPRINT_RE.exec(text.trim().toLowerCase());
  if (!m) return null;
  const [, flags, hex] = m;
  const semantic = flags[0] === "s";
  if (!semantic && flags[0] !== "x") return null;
  const rest = flags.slice(1);
  // Flags appear once, in canonical order, and a/c only with s.
  const options: FingerprintOptions = {
    semantic,
    ignoreArrayOrder: rest.includes("a"),
    ignoreChildOrder: rest.includes("c"),
    ignoreKafkitoHeaders: rest.includes("h"),
  };
  if (fingerprintFlags(options) !== flags) return null;
  return { options, hex };
}

export type FingerprintResult = { ok: true; fingerprint: string } | { ok: false; reason: string };

export async function messageFingerprint(
  content: MessageContent,
  o: FingerprintOptions,
): Promise<FingerprintResult> {
  if (content.key.preview || content.value.preview) {
    return { ok: false, reason: "Only a preview of the value is available." };
  }
  if (o.semantic && (tooLargeForSemantic(content.key) || tooLargeForSemantic(content.value))) {
    return {
      ok: false,
      reason: "Too large for a semantic fingerprint (over 1 MB); use the exact view.",
    };
  }
  // crypto.subtle exists only in secure contexts (HTTPS, localhost).
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return { ok: false, reason: "Fingerprints need kafkito to be served over HTTPS." };
  const digest = await subtle.digest("SHA-256", fingerprintInput(content, o));
  const hex = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
  return { ok: true, fingerprint: `${PREFIX}:${fingerprintFlags(o)}:${hex}` };
}

/**
 * The bytes a fingerprint hashes: every field length-prefixed so that no two
 * different records serialize alike. Exported for the tests.
 */
export function fingerprintInput(
  content: MessageContent,
  o: FingerprintOptions,
): Uint8Array<ArrayBuffer> {
  const out = new ByteWriter();
  out.text(`${PREFIX}\n`);
  writePart(out, content.key, o);
  writePart(out, content.value, o);
  const keys = [...content.headers.keys()]
    .filter((k) => !(o.ignoreKafkitoHeaders && isKafkitoHeader(k)))
    .sort();
  out.text(`H${keys.length}\n`);
  for (const k of keys) {
    const h = content.headers.get(k);
    if (!h) continue;
    out.field("T", utf8(k));
    if (h.b64 !== undefined) out.field("B", fromBase64(h.b64));
    else out.field("T", utf8(h.text));
  }
  return out.bytes();
}

function writePart(out: ByteWriter, p: PartContent, o: FingerprintOptions) {
  if (p.kind === "null") return out.text("N\n");
  if (p.kind === "empty") return out.text("E\n");
  if (p.kind === "binary") return out.field("B", fromBase64(p.data));
  const canonical = o.semantic ? canonicalPart(p, o) : null;
  out.field("T", utf8(canonical ?? p.data));
}

const encoder = new TextEncoder();

function utf8(s: string): Uint8Array {
  return encoder.encode(s);
}

function fromBase64(b64: string): Uint8Array {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

class ByteWriter {
  private chunks: Uint8Array[] = [];
  private size = 0;

  text(s: string) {
    this.push(utf8(s));
  }

  /** `<tag><length>:<bytes>\n` */
  field(tag: string, bytes: Uint8Array) {
    this.text(`${tag}${bytes.length}:`);
    this.push(bytes);
    this.text("\n");
  }

  private push(b: Uint8Array) {
    this.chunks.push(b);
    this.size += b.length;
  }

  bytes(): Uint8Array<ArrayBuffer> {
    const out = new Uint8Array(this.size);
    let at = 0;
    for (const c of this.chunks) {
      out.set(c, at);
      at += c.length;
    }
    return out;
  }
}
