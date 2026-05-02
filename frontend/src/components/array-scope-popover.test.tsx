import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ArrayScopePopover } from "./array-scope-popover";

function setup(overrides: Partial<React.ComponentProps<typeof ArrayScopePopover>> = {}) {
  const onApply = vi.fn();
  const onCancel = vi.fn();
  render(
    <ArrayScopePopover
      arrayPath="$.prices"
      arrayLength={3}
      indexLeafPath="$.prices[0].customerNumber"
      starLeafPath="$.prices[*].customerNumber"
      onApply={onApply}
      onCancel={onCancel}
      {...overrides}
    />,
  );
  return { onApply, onCancel };
}

describe("ArrayScopePopover", () => {
  it("renders both options with their resolved paths", () => {
    setup();
    expect(screen.getByText("$.prices[0].customerNumber")).toBeInTheDocument();
    expect(screen.getByText("$.prices[*].customerNumber")).toBeInTheDocument();
  });

  it("defaults to ANY (star)", () => {
    setup();
    const star = screen.getByLabelText(/All entries/i) as HTMLInputElement;
    expect(star.checked).toBe(true);
  });

  it("calls onApply with 'star' when ANY is selected and Apply is clicked", () => {
    const { onApply } = setup();
    fireEvent.click(screen.getByRole("button", { name: /Apply/i }));
    expect(onApply).toHaveBeenCalledWith("star");
  });

  it("calls onApply with 'index' after switching to THIS index", () => {
    const { onApply } = setup();
    fireEvent.click(screen.getByLabelText(/This index/i));
    fireEvent.click(screen.getByRole("button", { name: /Apply/i }));
    expect(onApply).toHaveBeenCalledWith("index");
  });

  it("calls onCancel on Escape", () => {
    const { onCancel } = setup();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onCancel).toHaveBeenCalled();
  });

  it("applies on Enter key", () => {
    const { onApply } = setup();
    fireEvent.keyDown(window, { key: "Enter" });
    expect(onApply).toHaveBeenCalledWith("star");
  });

  it("shows the array length in the header", () => {
    setup({ arrayLength: 17 });
    expect(screen.getByText(/17 entries/i)).toBeInTheDocument();
  });
});
