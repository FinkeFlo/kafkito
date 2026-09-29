import { describe, expect, it } from "vitest";
import { detectSearchMode } from "./search-mode-detect";

const enc = (...encodings: string[]) => encodings.map((value_encoding) => ({ value_encoding }));

describe("detectSearchMode", () => {
  it("keeps text search for an empty sample", () => {
    expect(detectSearchMode([])).toBe("contains");
  });

  it("keeps text search when nothing is structured", () => {
    expect(detectSearchMode(enc("text", "binary", "empty", "null"))).toBe("contains");
  });

  it("picks JSONPath when JSON dominates", () => {
    expect(detectSearchMode(enc("json", "json", "text", "xml"))).toBe("jsonpath");
  });

  it("picks XPath when XML dominates", () => {
    expect(detectSearchMode(enc("xml", "xml", "json", "text"))).toBe("xpath");
  });

  it("breaks a JSON/XML tie towards JSONPath", () => {
    expect(detectSearchMode(enc("xml", "json"))).toBe("jsonpath");
  });

  it("picks a structured mode even if text outnumbers it", () => {
    expect(detectSearchMode(enc("text", "text", "text", "json"))).toBe("jsonpath");
  });
});
