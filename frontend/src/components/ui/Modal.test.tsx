import { describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Modal } from "./Modal";

const nextFrame = () =>
  act(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));

describe("Modal initial focus", () => {
  it("moves focus to the first focusable element", async () => {
    render(
      <Modal open onClose={() => {}} title="t">
        <input aria-label="first" />
        <input aria-label="second" />
      </Modal>,
    );
    await nextFrame();
    expect(screen.getByLabelText("first")).toHaveFocus();
  });

  it("keeps focus on a field that already took it", async () => {
    render(
      <Modal open onClose={() => {}} title="t">
        <input aria-label="first" readOnly />
        {/* biome-ignore lint/a11y/noAutofocus: the behaviour under test */}
        <input aria-label="second" autoFocus />
      </Modal>,
    );
    await nextFrame();
    expect(screen.getByLabelText("second")).toHaveFocus();
  });
});

describe("Modal as a form", () => {
  it("submits from Enter in a field and from the submit button in actions", async () => {
    const onSubmit = vi.fn();
    render(
      <Modal
        open
        onClose={() => {}}
        title="t"
        onSubmit={onSubmit}
        actions={<button type="submit">Save</button>}
      >
        <input aria-label="first" />
        <input aria-label="second" />
      </Modal>,
    );
    await userEvent.type(screen.getByLabelText("second"), "x{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });
});
