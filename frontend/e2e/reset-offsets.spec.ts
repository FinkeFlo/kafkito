import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const GROUP = "e2e-idle-group";

test.describe("Reset Offsets walk (Q-001 fixture)", () => {
  test("opens, requires partition, requires confirm phrase, aborts cleanly", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/groups`);

    const groupRow = page.getByRole("row", { name: new RegExp(GROUP) });
    await expect(groupRow).toBeVisible();

    await groupRow.getByRole("button", { name: /reset offsets/i }).click();

    const dialog = page.getByRole("dialog", { name: /reset offsets/i });
    await expect(dialog).toBeVisible();

    const commit = dialog.getByRole("button", { name: /^commit$|^reset$/i });
    await expect(commit).toBeDisabled();
    await expect(dialog).toContainText(/pick at least one partition/i);

    await dialog.getByRole("checkbox", { name: /partition 0/i }).check();
    await expect(commit).toBeEnabled();

    await commit.click();

    const confirm = page.getByRole("dialog", { name: /confirm/i });
    await expect(confirm).toBeVisible();
    const confirmButton = confirm.getByRole("button", { name: /confirm|reset/i });
    await expect(confirmButton).toBeDisabled();

    const phraseInput = confirm.getByRole("textbox");
    await phraseInput.fill(GROUP.slice(0, 5));
    await expect(confirmButton).toBeDisabled();
    await phraseInput.fill(GROUP);
    await expect(confirmButton).toBeEnabled();

    await page.keyboard.press("Escape");
    await expect(confirm).toBeHidden();
  });
});
