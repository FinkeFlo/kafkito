import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FilterSelect } from "./FilterSelect";

const OPTIONS = [
  { value: "any", label: "Any" },
  { value: "small", label: "Small" },
] as const;

describe("FilterSelect", () => {
  it("is named by its visible label and reports the chosen value", async () => {
    const onChange = vi.fn();
    render(<FilterSelect label="Size" value="any" options={OPTIONS} onChange={onChange} />);
    await userEvent.selectOptions(screen.getByRole("combobox", { name: /size/i }), "small");
    expect(onChange).toHaveBeenCalledWith("small");
  });

  it("explains why it is disabled", () => {
    render(
      <FilterSelect
        label="Size"
        value="any"
        options={OPTIONS}
        onChange={() => {}}
        disabledReason="Size is not reported"
      />,
    );
    const select = screen.getByRole("combobox", { name: /size/i });
    expect(select).toBeDisabled();
    expect(select).toHaveAccessibleDescription("Size is not reported");
  });
});
