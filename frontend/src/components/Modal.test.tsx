import { describe, expect, it } from "vitest";
import { act, render, screen } from "@testing-library/react";
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
