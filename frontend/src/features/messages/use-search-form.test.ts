import { afterEach, describe, expect, it } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { useSearchForm } from "./use-search-form";

const enc = (...encodings: string[]) => encodings.map((value_encoding) => ({ value_encoding }));
const JSON_MSGS = enc("json", "json", "text");
const XML_MSGS = enc("xml", "xml");

function setup(topic = "orders") {
  return renderHook(({ topic }) => useSearchForm(topic), { initialProps: { topic } });
}

afterEach(cleanup);

describe("useSearchForm preselectMode", () => {
  it("starts with text search", () => {
    expect(setup().result.current.mode).toBe("contains");
  });

  it("preselects JSONPath from JSON messages", () => {
    const { result } = setup();
    act(() => result.current.preselectMode(JSON_MSGS));
    expect(result.current.mode).toBe("jsonpath");
  });

  it("preselects XPath from XML messages", () => {
    const { result } = setup();
    act(() => result.current.preselectMode(XML_MSGS));
    expect(result.current.mode).toBe("xpath");
  });

  it("falls back to text search when nothing is loaded", () => {
    const { result } = setup();
    act(() => result.current.preselectMode(JSON_MSGS));
    act(() => result.current.preselectMode([]));
    expect(result.current.mode).toBe("contains");
  });

  it("keeps a manual choice on later preselects for the same topic", () => {
    const { result } = setup();
    act(() => result.current.preselectMode(JSON_MSGS));
    act(() => result.current.setMode("contains"));
    act(() => result.current.preselectMode(JSON_MSGS));
    expect(result.current.mode).toBe("contains");
  });

  it("detects again after switching to another topic", () => {
    const { result, rerender } = setup("orders");
    act(() => result.current.setMode("js"));
    rerender({ topic: "invoices" });
    act(() => result.current.preselectMode(XML_MSGS));
    expect(result.current.mode).toBe("xpath");
  });
});
