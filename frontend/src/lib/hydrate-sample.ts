import { base64ToUtf8, fetchMessageRawBase64, type Message } from "./api";
import { isJsonLikeEncoding } from "./format";

/**
 * Upper bound on the raw size of a sample value this helper will download.
 *
 * PathSense only parses these values to collect field paths — it never
 * renders them — so the limit is about not pulling tens of megabytes over
 * the wire for a background suggestion feature, not about render cost.
 * Samples above the limit still contribute whatever fields survived the
 * 64 KB truncation.
 */
export const MAX_HYDRATE_VALUE_BYTES = 4 * 1024 * 1024;

/** Value encoding a PathSense tree can parse — one per search mode. */
export type HydratableEncoding = "json" | "xml";

/**
 * Whether a sample value is too large to be hydrated, and therefore reaches
 * the path-tree builders as the 64 KB preview only.
 *
 * Such a preview is almost always cut mid-structure and fails to parse, so
 * the builder yields nothing — which must not be reported to the user as
 * "isn't JSON": the value is perfectly valid, it is only too large to scan.
 */
export function isTooLargeToScan(m: Message): boolean {
  return m.value_truncated === true && (m.value_size_bytes ?? 0) > MAX_HYDRATE_VALUE_BYTES;
}

/**
 * Hydrates truncated JSON/XML sample messages with their full raw value, so
 * PathSense's field-path tree isn't missing fields that only appear past the
 * 64 KB truncation boundary of the `/sample` endpoint (or, for messages cut
 * mid-structure, isn't losing the message's fields entirely, since truncated
 * JSON/XML usually fails to parse). Falls back to the truncated preview on fetch
 * failure — e.g. the full value exceeds the raw-download cap — so the message
 * still contributes whatever fields survived truncation instead of being
 * dropped.
 *
 * Schema-Registry values decoded to JSON (Avro, JSON Schema) count as JSON:
 * the raw-download endpoint serves them decoded, like the sample.
 *
 * Masked values are skipped too: the server refuses their raw download, and
 * the suggestions must not be built from anything but the masked rendering.
 *
 * `encoding` must match what the caller's tree can actually consume. Only
 * values of that encoding are fetched: hydrating an XML sample for the JSON
 * tree (or vice versa) is never useful — the other builder discards it — and
 * each fetch costs up to MAX_HYDRATE_VALUE_BYTES.
 */
export async function hydrateTruncatedSampleMessages(
  cluster: string,
  topic: string,
  messages: Message[],
  signal?: AbortSignal,
  encoding: HydratableEncoding = "json",
): Promise<Message[]> {
  return Promise.all(
    messages.map(async (m) => {
      const matchesTree =
        encoding === "json" ? isJsonLikeEncoding(m.value_encoding) : m.value_encoding === encoding;
      const needsHydration =
        m.value_truncated === true &&
        matchesTree &&
        !m.masked &&
        (m.value_size_bytes ?? 0) <= MAX_HYDRATE_VALUE_BYTES;
      if (!needsHydration) return m;
      try {
        const base64 = await fetchMessageRawBase64(cluster, topic, m.partition, m.offset, signal);
        return { ...m, value: base64ToUtf8(base64), value_truncated: false };
      } catch {
        return m;
      }
    }),
  );
}
