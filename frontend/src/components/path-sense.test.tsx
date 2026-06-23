import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PathSense } from "./path-sense";
import type { PathInfo, PathTree } from "@/lib/path-tree";

// PathTree is a Map<string, PathInfo>; an empty Map means toRows(tree)
// returns no rows so the dropdown shows the manual-entry hint.
const emptyTree: PathTree = new Map();

function makeTree(entries: Array<[string, Partial<PathInfo>]>): PathTree {
  const t: PathTree = new Map();
  for (const [k, v] of entries) {
    t.set(k, {
      type: "string",
      sampleValues: [],
      distinctCount: 0,
      fromN: 1,
      ...v,
    });
  }
  return t;
}

describe("PathSense", () => {
  it("opens on focus and shows top entries", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([
          ["$.orderId", { type: "string", sampleValues: ["A1"] }],
          ["$.amount", { type: "number", sampleValues: [10] }],
        ])}
        value=""
        onChange={() => {}}
        onPick={onPick}
      />,
    );

    await user.click(screen.getByRole("combobox"));

    expect(screen.getByText("$.orderId")).toBeInTheDocument();
    expect(screen.getByText("$.amount")).toBeInTheDocument();
  });

  it("filters as the user types", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([
          ["$.orderId", { type: "string" }],
          ["$.customerName", { type: "string" }],
        ])}
        value=""
        onChange={onChange}
        onPick={() => {}}
      />,
    );

    const input = screen.getByRole("combobox");
    await user.click(input);
    await user.type(input, "cust");

    expect(screen.getByText("$.customerName")).toBeInTheDocument();
    expect(screen.queryByText("$.orderId")).not.toBeInTheDocument();
  });

  it("calls onPick when an entry is clicked", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([["$.orderId", { type: "string" }]])}
        value=""
        onChange={() => {}}
        onPick={onPick}
      />,
    );

    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByText("$.orderId"));

    expect(onPick).toHaveBeenCalledWith("$.orderId", expect.anything());
  });

  it("toggles last array segment with Tab after a star path is selected", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([])}
        value="$.prices[*].customerNumber"
        onChange={onChange}
        onPick={() => {}}
      />,
    );

    const input = screen.getByRole("combobox");
    await user.click(input);
    await user.tab();

    expect(onChange).toHaveBeenCalledWith("$.prices[0].customerNumber");
  });

  it("toggles back to star with another Tab", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([])}
        value="$.prices[3].customerNumber"
        onChange={onChange}
        onPick={() => {}}
      />,
    );

    const input = screen.getByRole("combobox");
    await user.click(input);
    await user.tab();

    expect(onChange).toHaveBeenCalledWith("$.prices[*].customerNumber");
  });

  it("shows an empty-state hint when the tree is empty", async () => {
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([])}
        value=""
        onChange={() => {}}
        onPick={() => {}}
      />,
    );

    await user.click(screen.getByRole("combobox"));

    expect(screen.getByText(/enter path manually/i)).toBeInTheDocument();
  });

  it("closes on Escape", async () => {
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([["$.x", { type: "string" }]])}
        value=""
        onChange={() => {}}
        onPick={() => {}}
      />,
    );
    const input = screen.getByRole("combobox");
    await user.click(input);
    expect(screen.getByText("$.x")).toBeInTheDocument();

    await user.keyboard("{Escape}");

    expect(screen.queryByText("$.x")).not.toBeInTheDocument();
  });

  it("closes the dropdown when focus leaves the component", () => {
    render(
      <div>
        <PathSense tree={emptyTree} value="" onChange={vi.fn()} onPick={vi.fn()} />
        <button type="button">outside</button>
      </div>,
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(input).toHaveAttribute("aria-expanded", "true");

    fireEvent.blur(input, { relatedTarget: screen.getByText("outside") });

    expect(input).toHaveAttribute("aria-expanded", "false");
  });

  it("Tab toggles the array segment based on the freshly typed query, not the lagging value prop", () => {
    const onChange = vi.fn();
    render(
      <PathSense tree={emptyTree} value="a[0].b" onChange={onChange} onPick={vi.fn()} />,
    );
    const input = screen.getByRole("combobox");
    // Simulate the user typing a new array path that the parent has not yet echoed back.
    fireEvent.change(input, { target: { value: "items[2].sku" } });
    onChange.mockClear();

    fireEvent.keyDown(input, { key: "Tab" });

    // It must toggle "items[2]" -> "items[*]", derived from the typed query.
    expect(onChange).toHaveBeenCalledWith("items[*].sku");
  });
});
