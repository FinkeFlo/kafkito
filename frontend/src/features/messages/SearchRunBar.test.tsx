import { afterEach, describe, expect, it } from "vitest";
import { act, cleanup, render, renderHook, screen } from "@testing-library/react";
import { SearchRunBar } from "./SearchRunBar";
import { useSearchForm } from "./use-search-form";

afterEach(cleanup);

function searchButton(form: ReturnType<typeof useSearchForm>) {
  render(
    <SearchRunBar
      form={form}
      limit={50}
      searching={false}
      hasResult={false}
      onSearch={() => {}}
      onStop={() => {}}
      onClear={() => {}}
    />,
  );
  return screen.getByRole("button", { name: "Search" });
}

describe("SearchRunBar", () => {
  it("runs a key search without a path", () => {
    const { result } = renderHook(() => useSearchForm("orders"));
    act(() => result.current.setMode("key"));
    expect(searchButton(result.current)).toBeEnabled();
  });

  it("waits for a header key in header mode", () => {
    const { result } = renderHook(() => useSearchForm("orders"));
    act(() => result.current.setMode("header"));
    expect(searchButton(result.current)).toBeDisabled();
  });

  it("runs a header search once a header key is set", () => {
    const { result } = renderHook(() => useSearchForm("orders"));
    act(() => {
      result.current.setMode("header");
      result.current.setHeaderKey("x-trace");
    });
    expect(searchButton(result.current)).toBeEnabled();
  });

  it("still needs a path for JSONPath", () => {
    const { result } = renderHook(() => useSearchForm("orders"));
    act(() => result.current.setMode("jsonpath"));
    expect(searchButton(result.current)).toBeDisabled();
  });
});
