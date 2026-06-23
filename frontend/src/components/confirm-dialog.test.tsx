import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfirmDialog } from "./confirm-dialog";

afterEach(cleanup);

describe("ConfirmDialog", () => {
  it("stays open when onConfirm rejects", async () => {
    const onOpenChange = vi.fn();
    const onConfirm = vi.fn().mockRejectedValue(new Error("commit failed"));

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Commit new offsets?"
        confirmLabel="Commit reset"
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Commit reset" }));

    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("closes when onConfirm resolves", async () => {
    const onOpenChange = vi.fn();
    const onConfirm = vi.fn().mockResolvedValue(undefined);

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Commit new offsets?"
        confirmLabel="Commit reset"
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Commit reset" }));

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });
});
