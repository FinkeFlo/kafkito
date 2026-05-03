import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PathSense } from "./path-sense";
import type { PathInfo, PathTree } from "@/lib/path-tree";

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
});
