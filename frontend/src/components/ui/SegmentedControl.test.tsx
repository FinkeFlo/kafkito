import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { SegmentedControl } from "./SegmentedControl";

const OPTIONS = [
  { value: "exact", content: "Exact" },
  { value: "semantic", content: "Semantic" },
] as const;

describe("SegmentedControl", () => {
  it("is a named radio group with the current value checked", () => {
    render(
      <SegmentedControl
        label="Diff view"
        value="semantic"
        onChange={() => {}}
        options={[...OPTIONS]}
      />,
    );
    expect(screen.getByRole("group", { name: "Diff view" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Semantic" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Exact" })).not.toBeChecked();
  });

  it("reports the picked value", () => {
    const onChange = vi.fn();
    render(
      <SegmentedControl
        label="Diff view"
        value="semantic"
        onChange={onChange}
        options={[...OPTIONS]}
      />,
    );
    fireEvent.click(screen.getByRole("radio", { name: "Exact" }));
    expect(onChange).toHaveBeenCalledWith("exact");
  });
});
