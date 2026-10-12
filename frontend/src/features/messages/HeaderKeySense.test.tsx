import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HeaderKeySense } from "./HeaderKeySense";

const stats = [
  { key: "x-correlation-id", count: 48 },
  { key: "content-type", count: 50 },
  { key: "x-tenant", count: 3 },
];

describe("HeaderKeySense", () => {
  it("lists the header keys with the number of loaded messages carrying each", async () => {
    const user = userEvent.setup();
    render(<HeaderKeySense value="" onChange={() => {}} stats={stats} total={50} />);

    await user.click(screen.getByRole("combobox"));

    const row = screen.getByText("x-correlation-id").closest("[role=option]");
    expect(row?.textContent).toBe("x-correlation-id48 of 50 messages");
    expect(screen.getByText("content-type")).toBeInTheDocument();
  });

  it("filters on a substring and puts the picked key into the input", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <HeaderKeySense value="" onChange={onChange} stats={stats} total={50} />,
    );

    await user.click(screen.getByRole("combobox"));
    rerender(<HeaderKeySense value="tenant" onChange={onChange} stats={stats} total={50} />);

    expect(screen.queryByText("content-type")).not.toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: /tenant/ }));
    expect(onChange).toHaveBeenLastCalledWith("x-tenant");
    expect(screen.queryByRole("option", { name: /tenant/ })).not.toBeInTheDocument();
    expect(screen.getByRole("combobox")).toHaveFocus();
  });

  it("says to type the key when the loaded messages carry no headers", async () => {
    const user = userEvent.setup();
    render(<HeaderKeySense value="" onChange={() => {}} stats={[]} total={0} />);

    await user.click(screen.getByRole("combobox"));

    expect(screen.getByText("No headers in the loaded messages. Type the key.")).toBeVisible();
  });

  it("moves through the suggestions with the arrow keys and picks with Enter", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<HeaderKeySense value="" onChange={onChange} stats={stats} total={50} />);

    const input = screen.getByRole("combobox");
    await user.click(input);
    expect(input).toHaveAttribute("aria-controls", screen.getByRole("listbox").id);
    await user.keyboard("{ArrowDown}{ArrowDown}");

    const second = screen.getByRole("option", { name: /content-type/ });
    expect(second).toHaveAttribute("aria-selected", "true");
    expect(input).toHaveAttribute("aria-activedescendant", second.id);
    expect(input).toHaveFocus();

    await user.keyboard("{Enter}");
    expect(onChange).toHaveBeenLastCalledWith("content-type");
    expect(input).toHaveAttribute("aria-expanded", "false");
    expect(input).toHaveFocus();
  });

  it("keeps the suggestions out of the tab order", async () => {
    const user = userEvent.setup();
    render(<HeaderKeySense value="" onChange={() => {}} stats={stats} total={50} />);

    await user.click(screen.getByRole("combobox"));
    for (const option of screen.getAllByRole("option")) {
      expect(option).toHaveAttribute("tabindex", "-1");
    }
  });

  it("closes with Escape", async () => {
    const user = userEvent.setup();
    render(<HeaderKeySense value="" onChange={() => {}} stats={stats} total={50} />);

    const input = screen.getByRole("combobox");
    await user.click(input);
    expect(input).toHaveAttribute("aria-expanded", "true");
    await user.keyboard("{Escape}");
    expect(input).toHaveAttribute("aria-expanded", "false");
  });
});
