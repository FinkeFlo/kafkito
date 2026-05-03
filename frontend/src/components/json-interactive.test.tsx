import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  ARRAY_COLLAPSE_THRESHOLD,
  JsonInteractive,
  SIZE_LIMIT_BYTES,
} from "./json-interactive";

describe("JsonInteractive", () => {
  it("renders scalar values clickably and reports trail + literal on click", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(<JsonInteractive value={{ orderId: "A1" }} onPick={onPick} />);

    await user.click(screen.getByText('"A1"'));

    expect(onPick).toHaveBeenCalledWith(
      [{ kind: "key", name: "orderId" }],
      "A1",
      [0],
    );
  });

  it("clicking a key reports trail with undefined leaf", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(<JsonInteractive value={{ orderId: "A1" }} onPick={onPick} />);

    await user.click(screen.getByText(/orderId/));

    expect(onPick).toHaveBeenCalledWith(
      [{ kind: "key", name: "orderId" }],
      undefined,
      [0],
    );
  });

  it("clicking inside an array fires a trail with index token and array length", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(
      <JsonInteractive
        value={{ prices: [{ x: 1 }, { x: 2 }] }}
        onPick={onPick}
      />,
    );

    await user.click(screen.getByText("1"));

    expect(onPick).toHaveBeenCalledWith(
      [
        { kind: "key", name: "prices" },
        { kind: "index", value: 0 },
        { kind: "key", name: "x" },
      ],
      1,
      [0, 2, 0],
    );
  });

  it(`collapses arrays with more than ${ARRAY_COLLAPSE_THRESHOLD} items by default`, () => {
    const arrayLength = ARRAY_COLLAPSE_THRESHOLD + 150;
    const arr = Array.from({ length: arrayLength }, (_, i) => i);
    render(<JsonInteractive value={{ items: arr }} onPick={() => {}} />);

    expect(
      screen.getByRole("button", { name: new RegExp(`Show all ${arrayLength}`, "i") }),
    ).toBeInTheDocument();
    expect(screen.queryByText(String(arrayLength - 1))).not.toBeInTheDocument();
  });

  it.each<[string, number]>([
    ["at threshold", ARRAY_COLLAPSE_THRESHOLD],
    ["below threshold", ARRAY_COLLAPSE_THRESHOLD - 1],
  ])("does not collapse arrays %s (no Show all button)", (_label, arrayLength) => {
    const arr = Array.from({ length: arrayLength }, (_, i) => i);
    render(<JsonInteractive value={{ items: arr }} onPick={() => {}} />);

    expect(screen.queryByRole("button", { name: /Show all/i })).not.toBeInTheDocument();
    expect(screen.getByText(String(arrayLength - 1))).toBeInTheDocument();
  });

  it("expands a collapsed array on click", async () => {
    const arrayLength = ARRAY_COLLAPSE_THRESHOLD + 150;
    const arr = Array.from({ length: arrayLength }, (_, i) => i);
    const user = userEvent.setup();
    render(<JsonInteractive value={{ items: arr }} onPick={() => {}} />);

    await user.click(
      screen.getByRole("button", { name: new RegExp(`Show all ${arrayLength}`, "i") }),
    );

    expect(screen.getByText(String(arrayLength - 1))).toBeInTheDocument();
  });

  it("falls back to <pre> for messages over the size limit", () => {
    const overLimit = SIZE_LIMIT_BYTES + 100_000;
    const giant = "x".repeat(overLimit);
    render(<JsonInteractive value={{ blob: giant }} onPick={() => {}} />);

    expect(screen.getByText(/Message too large for interactive mode/i)).toBeInTheDocument();
  });
});
