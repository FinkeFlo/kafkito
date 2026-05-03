import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const GROUP = "e2e-idle-group";

test.describe("Reset Offsets walk (Q-001 fixture)", () => {
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

    const commitButton = modal.getByRole("button", { name: /commit reset/i });
    await expect(commitButton).toBeDisabled();
    await expect(modal).toContainText(/pick at least one partition/i);

    // Partition checkbox is intentionally sr-only (visual chip lives on the
    // label). Playwright's actuator targets the input bbox which is clipped,
    // so { force: true } skips actionability — the test still verifies the
    // labelled checkbox toggles, which is the user-observable contract.
    await modal
      .getByRole("checkbox", { name: /^p0$/ })
      .check({ force: true });
    await expect(commitButton).toBeEnabled();

    await commitButton.click();

    const confirm = page.getByRole("dialog", { name: /commit new offsets/i });
    await expect(confirm).toBeVisible();
    const confirmCommit = confirm.getByRole("button", { name: /commit reset/i });
    await expect(confirmCommit).toBeDisabled();

    const phraseInput = confirm.getByRole("textbox");
    await phraseInput.fill(GROUP.slice(0, 5));
    await expect(confirmCommit).toBeDisabled();
    await phraseInput.fill(GROUP);
    await expect(confirmCommit).toBeEnabled();

    await page.keyboard.press("Escape");
    await expect(confirm).toBeHidden();
  });
});
