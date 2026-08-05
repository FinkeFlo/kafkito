// produce-encoding — maps a *consumed* message field back to the (value,
// encoding) pair the produce API needs in order to reproduce the original
// record byte-for-byte.
//
// MUST STAY IN SYNC WITH `produceEncodingFor` in internal/server/topic_copy.go.
// Both implement the same fidelity rules for two different callers of the same
// produce API: the Go copy runs server-side for bulk topic copies, this module
// runs client-side for the single-message "Replay to…" dialog. Diverging means
// one of the two silently corrupts payloads, which is exactly the bug this
// module was introduced to fix. When you change the rules here, change them
// there too (and vice versa).
//
// Background — what the consumer emits (pkg/kafka/consumer.go, decodeBytes):
//   "null"   nil payload, rendered = ""            → faithful reproduction is nil
//   "empty"  zero-length payload, rendered = ""    → must NOT become nil
//   "text"   UTF-8, rendered = the exact string
//   "json"   UTF-8 JSON, rendered = the exact string
//   "binary" non-UTF-8; rendered is a *truncated* hex preview ("0x" + up to
//            64 bytes of hex) — only `b64` holds the full bytes
//   "avro" / "json_schema" / "protobuf": a Schema-Registry decoder replaced the
//            rendering with decoded JSON and cleared `b64` (applySRDecoder),
//            so the original Confluent wire-format bytes (magic byte + schema
//            id + payload) are gone and cannot be reproduced at all.

import type { Message } from "./api";

/** Encodings accepted by the produce API (see kafkapkg.ProduceRequest). */
export type ProduceEncoding = "text" | "base64" | "empty";

/** Result of mapping a consumed field to a produce payload. */
export type ProducePayload = { value: string; encoding: ProduceEncoding };

/** Encodings whose original bytes were discarded by the SR decoder. */
const SR_ENCODINGS = ["avro", "json_schema", "protobuf"] as const;

function isSREncoding(encoding: string): boolean {
  return (SR_ENCODINGS as readonly string[]).includes(encoding);
}

/**
 * Maps a consumed field (rendered, b64, encoding) to the (value, encoding)
 * pair the produce API needs for a byte-for-byte reproduction.
 * Returns null when the original bytes are unrecoverable from a Message.
 */
export function produceEncodingFor(
  rendered: string | undefined,
  b64: string | undefined,
  encoding: string,
): ProducePayload | null {
  switch (encoding) {
    case "avro":
    case "json_schema":
    case "protobuf":
      // applySRDecoder overwrote `rendered` with decoded JSON and cleared the
      // base64 form: nothing faithful left to send.
      return null;

    case "binary":
      // `rendered` is only a truncated hex preview — never send it. The full
      // bytes live in `b64`.
      if (b64) return { value: b64, encoding: "base64" };
      // A "binary" field is non-empty by construction (decodeBytes reports
      // zero-length payloads as "empty"), so a missing b64 means the bytes are
      // simply not available. Refuse rather than produce the preview string.
      // The `rendered === ""` guard only covers a hypothetical future producer
      // of "binary" with no bytes at all.
      if (!rendered) return { value: "", encoding: "empty" };
      return null;

    case "empty":
      // Zero-length, but NOT nil: "text" with an empty value would be decoded
      // as a nil payload server-side, i.e. a tombstone.
      return { value: "", encoding: "empty" };

    case "null":
      // A nil payload IS the faithful reproduction here — "text" + "" is
      // exactly how the produce API expresses it.
      return { value: "", encoding: "text" };

    default:
      // "text", "json" and anything unknown. Unknown encodings deliberately
      // fall back to the rendered string rather than to null: every encoding
      // the backend has ever emitted apart from "binary" and the SR formats
      // renders the payload verbatim, so pass-through is the reproduction that
      // is right for the whole known family and stays right for any future
      // *textual* encoding. Blocking instead would break replay for encodings
      // that work fine; a future encoding that lossily renders its payload
      // must therefore be added to the cases above explicitly (as "binary" and
      // the SR formats were) — that is the contract this default relies on.
      return { value: rendered ?? "", encoding: "text" };
  }
}

/** Why a message cannot be replayed faithfully. Shown to the user verbatim. */
export type ReplayBlocker = { reason: string };

/**
 * Fields of a Message that decide replayability. `key`/`value` and their
 * base64 forms are optional so callers can pass a full Message (they are used
 * only to detect a "binary" field whose raw bytes are missing).
 */
type ReplayCandidate = Pick<
  Message,
  "key_encoding" | "value_encoding" | "masked" | "key_sr" | "value_sr"
> &
  Partial<Pick<Message, "key" | "key_b64" | "value" | "value_b64">>;

/**
 * Returns null when the message can be replayed byte-for-byte, otherwise the
 * reason it cannot. Blocks on masked records (the rendering the UI holds is
 * redacted, not the real payload) and on a key or value whose original bytes
 * are unrecoverable.
 */
export function replayBlocker(m: ReplayCandidate): ReplayBlocker | null {
  if (m.masked) {
    return {
      reason:
        "Masked message: the value shown is redacted, replaying it would write the redacted text.",
    };
  }

  // Report the value first — it is what users look at.
  const valueReason = fieldBlockReason(
    "value",
    m.value,
    m.value_b64,
    m.value_encoding,
    !!m.value_sr,
  );
  if (valueReason) return { reason: valueReason };

  const keyReason = fieldBlockReason("key", m.key, m.key_b64, m.key_encoding, !!m.key_sr);
  if (keyReason) return { reason: keyReason };

  return null;
}

/**
 * Reason the given field cannot be reproduced, or null if it can. The SR meta
 * only refines the wording — whether the field is replayable at all is decided
 * by produceEncodingFor, so the two can never disagree.
 */
function fieldBlockReason(
  field: "key" | "value",
  rendered: string | undefined,
  b64: string | undefined,
  encoding: string,
  hasSRMeta: boolean,
): string | null {
  if (produceEncodingFor(rendered, b64, encoding) !== null) return null;
  if (isSREncoding(encoding) || hasSRMeta) {
    return `Schema-Registry-decoded ${field}: the original wire-format bytes are not available, so this message cannot be replayed byte-for-byte.`;
  }
  return `Binary ${field}: only a truncated preview of the raw bytes is available, so this message cannot be replayed byte-for-byte.`;
}
