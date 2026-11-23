import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const GROUP = "e2e-idle-group";

test.describe("Reset Offsets walk (idle group fixture)", () => {
  test("opens, requires partition, requires confirm phrase, aborts cleanly", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/groups`);

    const groupTrigger = page.getByRole("button", { name: new RegExp(GROUP) });
    await expect(groupTrigger).toBeVisible();
    await groupTrigger.click();

    const resetTrigger = page.getByRole("button", { name: /^reset offsets/i });
    await expect(resetTrigger).toBeVisible();
    await resetTrigger.click();

    const modal = page.getByRole("dialog", { name: /reset offsets/i });
    await expect(modal).toBeVisible();

    const reviewButton = modal.getByRole("button", { name: /review reset/i });
    // All partitions are selected by default, so review is enabled up-front.
    await expect(reviewButton).toBeEnabled();

    // Deselecting every partition re-enables the guard.
    await modal.getByRole("button", { name: /^none$/ }).click();
    await expect(reviewButton).toBeDisabled();
    await expect(modal).toContainText(/pick at least one partition/i);

    // Re-select all partitions to proceed.
    await modal.getByRole("button", { name: /^all$/ }).click();
    await expect(reviewButton).toBeEnabled();

    await reviewButton.click();

    // The review replaces the form inside the same dialog.
    const confirmCommit = modal.getByRole("button", { name: /commit reset/i });
    await expect(confirmCommit).toBeDisabled();

    const phraseInput = modal.getByRole("textbox", { name: /type the group name/i });
    await phraseInput.fill(GROUP.slice(0, 5));
    await expect(confirmCommit).toBeDisabled();
    await phraseInput.fill(GROUP);
    await expect(confirmCommit).toBeEnabled();

    // Escape steps back to the configuration; a second Escape closes the dialog.
    await page.keyboard.press("Escape");
    await expect(phraseInput).toBeHidden();
    await expect(reviewButton).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(modal).toBeHidden();
  });
});
