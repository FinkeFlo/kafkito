import type { Message, SearchMode } from "@/lib/api";

/**
 * Picks the search mode that fits a topic's payloads from the detected value
 * encodings of a sample: any JSON or XML value suggests a structured mode,
 * JSONPath when JSON values are at least as many as XML ones, else XPath.
 * A sample without either keeps plain text search.
 */
export function detectSearchMode(messages: Pick<Message, "value_encoding">[]): SearchMode {
  let json = 0;
  let xml = 0;
  for (const m of messages) {
    if (m.value_encoding === "json") json++;
    else if (m.value_encoding === "xml") xml++;
  }
  if (json === 0 && xml === 0) return "contains";
  return json >= xml ? "jsonpath" : "xpath";
}
