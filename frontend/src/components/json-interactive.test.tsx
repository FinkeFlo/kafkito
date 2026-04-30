import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { JsonInteractive } from "./json-interactive";

describe("JsonInteractive", () => {
  it("renders scalar values clickably and reports trail + literal on click", () => {
    const onPick = vi.fn();
    render(<JsonInteractive value={{ orderId: "A1" }} onPick={onPick} />);
    fireEvent.click(screen.getByText('"A1"'));
    expect(onPick).toHaveBeenCalledWith(
      [{ kind: "key", name: "orderId" }],
      "A1",
      [0],
    );
  });

  it("clicking a key reports trail with undefined leaf", () => {
    const onPick = vi.fn();
    render(<JsonInteractive value={{ orderId: "A1" }} onPick={onPick} />);
    fireEvent.click(screen.getByText(/orderId/));
    expect(onPick).toHaveBeenCalledWith(
      [{ kind: "key", name: "orderId" }],
      undefined,
      [0],
    );
  });

  it("clicking inside an array fires a trail with index token and array length", () => {
    const onPick = vi.fn();
    render(
      <JsonInteractive
        value={{ prices: [{ x: 1 }, { x: 2 }] }}
        onPick={onPick}
      />,
    );
    fireEvent.click(screen.getByText("1"));
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

  it("collapses arrays with more than 100 items by default", () => {
    const arr = Array.from({ length: 250 }, (_, i) => i);
    render(<JsonInteractive value={{ items: arr }} onPick={() => {}} />);
    expect(screen.getByRole("button", { name: /Show all 250/i })).toBeInTheDocument();
    expect(screen.queryByText("249")).not.toBeInTheDocument();
  });

  it("expands a collapsed array on click", () => {
    const arr = Array.from({ length: 250 }, (_, i) => i);
    render(<JsonInteractive value={{ items: arr }} onPick={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: /Show all 250/i }));
    expect(screen.getByText("249")).toBeInTheDocument();
  });

  it("falls back to <pre> for messages over 1 MB", () => {
    const giant = "x".repeat(1_100_000);
    render(<JsonInteractive value={{ blob: giant }} onPick={() => {}} />);
    expect(screen.getByText(/zu groß für interaktiven Modus/i)).toBeInTheDocument();
  });
});
