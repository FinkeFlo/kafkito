import { base64ToUtf8, fetchMessageRawBase64, type Message } from "./api";
import { looksLikeJson } from "./looks-like-json";

/**
 * Hydrates any truncated JSON-looking sample message with its full raw
 * value, so PathSense's field-path tree isn't missing fields that only
 * appear past the 64 KB truncation boundary of the `/sample` endpoint (or,
 * for messages that are cut mid-structure, isn't losing the message's
 * fields entirely, since truncated JSON usually fails to parse). Falls back
 * to the truncated preview on fetch failure — e.g. the full value exceeds
 * the raw-download cap — so the message still contributes whatever fields
 * survived truncation instead of being dropped.
 *
 * Checks `looksLikeJson(m.value)` rather than `m.value_encoding === "json"`:
 * a value truncated to 64 KB is usually no longer *valid* JSON, so the
 * backend itself reports `value_encoding: "text"` for exactly the large
 * messages this function needs to catch.
 */
export async function hydrateTruncatedSampleMessages(
  cluster: string,
  topic: string,
  messages: Message[],
): Promise<Message[]> {
  return Promise.all(
    messages.map(async (m) => {
      if (!m.value_truncated || !looksLikeJson(m.value ?? "")) return m;
      try {
        const base64 = await fetchMessageRawBase64(
          cluster,
          topic,
          m.partition,
          m.offset,
        );
        return { ...m, value: base64ToUtf8(base64), value_truncated: false };
      } catch {
        return m;
      }
    }),
  );
}

