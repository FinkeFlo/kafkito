import type { Message, SearchMode } from "@/lib/api";

/**
 * Picks the search mode that fits a topic's payloads, by majority vote over
 * the detected value encodings of a sample: mostly JSON suggests JSONPath,
 * mostly XML suggests XPath (a tie goes to JSONPath), anything else keeps
 * plain text search.
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
