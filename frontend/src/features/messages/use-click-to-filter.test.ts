import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { useClickToFilter } from "./use-click-to-filter";
import { useSearchForm } from "./use-search-form";

function setup() {
  const openSearch = vi.fn();
  const hook = renderHook(() => {
    const form = useSearchForm("orders");
    return { form, click: useClickToFilter(form, openSearch) };
  });
  return { ...hook, openSearch };
}

const fields = ({ form }: ReturnType<typeof setup>["result"]["current"]) => ({
  mode: form.mode,
  path: form.path,
  headerKey: form.headerKey,
  op: form.op,
  needle: form.needle,
});

afterEach(cleanup);

describe("useClickToFilter", () => {
  it("turns the record key into a key search and opens the panel", () => {
    const { result, openSearch } = setup();
    act(() => result.current.click.handleFilter({ kind: "key", value: "ord-77" }));

    expect(openSearch).toHaveBeenCalled();
    expect(fields(result.current)).toMatchObject({ mode: "key", op: "eq", needle: "ord-77" });
    expect(result.current.click.showUndo).toBe(false);
  });

  it("offers undo when it replaced typed input, and undo restores the mode too", () => {
    const { result } = setup();
    act(() => {
      result.current.form.setMode("xpath");
      result.current.form.setPath("/order/id");
      result.current.form.setOp("regex");
      result.current.form.setNeedle("^A");
    });
    const before = fields(result.current);

    act(() => result.current.click.handleFilter({ kind: "key", value: "ord-77" }));
    expect(result.current.click.showUndo).toBe(true);

    act(() => result.current.click.undo());
    expect(fields(result.current)).toEqual(before);
    expect(result.current.click.showUndo).toBe(false);
  });
});
