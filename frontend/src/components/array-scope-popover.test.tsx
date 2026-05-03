import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ArrayScopePopover } from "./array-scope-popover";

function setup(overrides: Partial<React.ComponentProps<typeof ArrayScopePopover>> = {}) {
  const onApply = vi.fn();
  const onCancel = vi.fn();
  const user = userEvent.setup();
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
  return { onApply, onCancel, user };
}

describe("ArrayScopePopover", () => {
  it("renders both options with their resolved paths", () => {
    setup();

    expect(screen.getByText("$.prices[0].customerNumber")).toBeInTheDocument();
    expect(screen.getByText("$.prices[*].customerNumber")).toBeInTheDocument();
  });

  it("defaults to ANY (star)", () => {
    setup();

    expect(screen.getByLabelText(/All entries/i)).toBeChecked();
  });

  it("calls onApply with 'star' when ANY is selected and Apply is clicked", async () => {
    const { onApply, user } = setup();

    await user.click(screen.getByRole("button", { name: /Apply/i }));

    expect(onApply).toHaveBeenCalledWith("star");
  });

  it("calls onApply with 'index' after switching to THIS index", async () => {
    const { onApply, user } = setup();

    await user.click(screen.getByLabelText(/This index/i));
    await user.click(screen.getByRole("button", { name: /Apply/i }));

    expect(onApply).toHaveBeenCalledWith("index");
  });

  it("calls onCancel on Escape", async () => {
    const { onCancel, user } = setup();

    await user.keyboard("{Escape}");

    expect(onCancel).toHaveBeenCalled();
  });

  it("applies on Enter key", async () => {
    const { onApply, user } = setup();

    await user.keyboard("{Enter}");

    expect(onApply).toHaveBeenCalledWith("star");
  });

  it("shows the array length in the header", () => {
    setup({ arrayLength: 17 });

    expect(screen.getByText(/17 entries/i)).toBeInTheDocument();
  });
});
