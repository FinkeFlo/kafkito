import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { PathSense } from "./path-sense";
import type { PathTree } from "@/lib/path-tree";

function makeTree(entries: Array<[string, Partial<PathTree extends Map<string, infer V> ? V : never>]>): PathTree {
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
  it("opens on focus and shows top entries", () => {
    const onPick = vi.fn();
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
    fireEvent.focus(screen.getByRole("combobox"));
    expect(screen.getByText("$.orderId")).toBeInTheDocument();
    expect(screen.getByText("$.amount")).toBeInTheDocument();
  });

  it("filters as the user types", () => {
    const onChange = vi.fn();
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
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "cust" } });
    expect(screen.getByText("$.customerName")).toBeInTheDocument();
    expect(screen.queryByText("$.orderId")).not.toBeInTheDocument();
  });

  it("calls onPick when an entry is clicked", () => {
    const onPick = vi.fn();
    render(
      <PathSense
        tree={makeTree([["$.orderId", { type: "string" }]])}
        value=""
        onChange={() => {}}
        onPick={onPick}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(screen.getByText("$.orderId"));
    expect(onPick).toHaveBeenCalledWith("$.orderId", expect.anything());
  });

  it("toggles last array segment with Tab after a star path is selected", () => {
    const onChange = vi.fn();
    render(
      <PathSense
        tree={makeTree([])}
        value="$.prices[*].customerNumber"
        onChange={onChange}
        onPick={() => {}}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.keyDown(input, { key: "Tab" });
    expect(onChange).toHaveBeenCalledWith("$.prices[0].customerNumber");
  });

  it("toggles back to star with another Tab", () => {
    const onChange = vi.fn();
    render(
      <PathSense
        tree={makeTree([])}
        value="$.prices[3].customerNumber"
        onChange={onChange}
        onPick={() => {}}
      />,
    );
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Tab" });
    expect(onChange).toHaveBeenCalledWith("$.prices[*].customerNumber");
  });

  it("shows an empty-state hint when the tree is empty", () => {
    render(
      <PathSense
        tree={makeTree([])}
        value=""
        onChange={() => {}}
        onPick={() => {}}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(screen.getByText(/enter path manually/i)).toBeInTheDocument();
  });

  it("closes on Escape", () => {
    render(
      <PathSense
        tree={makeTree([["$.x", { type: "string" }]])}
        value=""
        onChange={() => {}}
        onPick={() => {}}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(screen.getByText("$.x")).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Escape" });
    expect(screen.queryByText("$.x")).not.toBeInTheDocument();
  });
});
