import { base64ToUtf8, fetchMessageRawBase64, type Message } from "./api";

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
 * Hydrates truncated JSON/XML sample messages with their full raw value, so
 * PathSense's field-path tree isn't missing fields that only appear past the
 * 64 KB truncation boundary of the `/sample` endpoint (or, for messages cut
 * mid-structure, isn't losing the message's fields entirely, since truncated
 * JSON/XML usually fails to parse). Falls back to the truncated preview on fetch
 * failure — e.g. the full value exceeds the raw-download cap — so the message
 * still contributes whatever fields survived truncation instead of being
 * dropped.
 *
 * Schema-Registry values are skipped: their `value` is the *decoded* JSON
 * rendering, while the raw-download endpoint returns the encoded wire bytes,
 * which would not parse as JSON.
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
      const needsHydration =
        m.value_truncated === true &&
        m.value_encoding === encoding &&
        !m.value_sr &&
        (m.value_size_bytes ?? 0) <= MAX_HYDRATE_VALUE_BYTES;
      if (!needsHydration) return m;
      try {
        const base64 = await fetchMessageRawBase64(
          cluster,
          topic,
          m.partition,
          m.offset,
          signal,
        );
        return { ...m, value: base64ToUtf8(base64), value_truncated: false };
      } catch {
        return m;
      }
    }),
  );
}
