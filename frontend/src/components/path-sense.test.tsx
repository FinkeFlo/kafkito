import { describe, expect, it, vi } from "vitest";
import { createEvent, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PathSense } from "./path-sense";
import type { PathInfo, PathTree } from "@/lib/path-tree";

// PathTree is a Map<string, PathInfo>; an empty Map means toRows(tree)
// returns no rows so the dropdown shows the manual-entry hint.
const emptyTree: PathTree = new Map();

function makeTree(entries: Array<[string, Partial<PathInfo>]>): PathTree {
  const t: PathTree = new Map();
  for (const [k, v] of entries) {
    t.set(k, { type: "string", ...v });
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
          ["$.orderId", { type: "string" }],
          ["$.amount", { type: "number" }],
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

  it("lists the field path and its type, and no payload data", async () => {
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([["$.status", { type: "string" }]])}
        value=""
        onChange={() => {}}
        onPick={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("combobox"));

    // Exact equality, not a substring check: it is what proves the row
    // carries nothing beyond the field name and its type — no sample value
    // and no distinct count.
    const row = screen.getByText("$.status").closest("button");
    expect(row?.textContent).toBe("$.statusstring");
  });

  it("passes the picked path's type, not a value, to onPick", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([["$.meta", { type: "object" }]])}
        value=""
        onChange={() => {}}
        onPick={onPick}
      />,
    );

    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByText("$.meta"));

    expect(onPick).toHaveBeenCalledWith("$.meta", "object");
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

    // The match is highlighted via <mark>, splitting "$.customerName" across
    // several text nodes, so match on the row's full text content instead of
    // an exact single-node string.
    expect(
      screen.getByText(
        (_, el) =>
          el?.classList.contains("font-mono") === true &&
          el.textContent === "$.customerName",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/orderId/)).not.toBeInTheDocument();
  });

  it("matches on a substring anywhere in the path, not just a prefix, and highlights the matched range", async () => {
    const user = userEvent.setup();
    render(
      <PathSense
        tree={makeTree([
          ["$.order.items[*].price", { type: "number" }],
          ["$.order.items[*].sku", { type: "string" }],
        ])}
        value=""
        onChange={() => {}}
        onPick={() => {}}
      />,
    );

    const input = screen.getByRole("combobox");
    await user.click(input);
    await user.type(input, "pric");

    expect(screen.getByText("pric", { selector: "mark" })).toBeInTheDocument();
    expect(screen.queryByText(/sku/)).not.toBeInTheDocument();
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

  it("does not rewrite XPath positional predicates on Tab when the toggle is off", () => {
    const onChange = vi.fn();
    render(
      <PathSense
        tree={emptyTree}
        value=""
        onChange={onChange}
        onPick={vi.fn()}
        arrayIndexToggle={false}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "//order/items/item[2]/@sku" } });
    onChange.mockClear();

    fireEvent.keyDown(input, { key: "Tab" });

    // `item[*]` is valid XPath but selects elements having a child *element*,
    // so the JSONPath "all entries" rewrite would silently change the query's
    // meaning — and for attribute-only <item sku=…/> it matches nothing.
    expect(onChange).not.toHaveBeenCalled();
  });

  it("lets Tab move focus out of the input when the toggle is off", () => {
    render(
      <PathSense
        tree={emptyTree}
        value=""
        onChange={vi.fn()}
        onPick={vi.fn()}
        arrayIndexToggle={false}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "//order/items/item[2]/@sku" } });

    const event = createEvent.keyDown(input, { key: "Tab" });
    fireEvent(input, event);

    // preventDefault would trap keyboard users in the Path field.
    expect(event.defaultPrevented).toBe(false);
  });

  it("forwards an id to the combobox input so an external label can target it", () => {
    render(
      <>
        <label htmlFor="search-path">Path</label>
        <PathSense
          id="search-path"
          tree={emptyTree}
          value=""
          onChange={vi.fn()}
          onPick={vi.fn()}
        />
      </>,
    );

    // Without the id the label would be a dangling reference in JSONPath
    // mode, where PathSense replaces the plain <input id="search-path">.
    expect(screen.getByLabelText("Path")).toBe(screen.getByRole("combobox"));
  });
});
