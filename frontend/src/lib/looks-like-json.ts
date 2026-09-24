/**
 * Mirrors the first half of the backend's own JSON-encoding heuristic
 * (`decodeBytes` in pkg/kafka/consumer.go): a value "looks like" JSON if its
 * first non-whitespace character is `{` or `[`. Used wherever we need this
 * check on a value the backend may have already truncated to 64 KB — after
 * truncation the value is usually no longer *valid* JSON (cut off
 * mid-structure), so the backend itself falls back to reporting
 * `value_encoding: "text"` for it. That means `value_encoding === "json"`
 * cannot be trusted to find large, truncated JSON messages; this cheaper,
 * truncation-tolerant check can.
 */
export function looksLikeJson(text: string): boolean {
  const trimmed = text.trimStart();
  return trimmed.length > 0 && (trimmed[0] === "{" || trimmed[0] === "[");
}
