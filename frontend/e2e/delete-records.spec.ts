import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-walk-target";

test.describe("Delete Records walk (Q-001 fixture)", () => {
  test("requires confirm phrase = topic name, aborts cleanly", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}`,
    );

    const trigger = page.getByRole("button", { name: /delete records/i });
    await expect(trigger).toBeVisible();
    await trigger.click();

    const confirm = page.getByRole("dialog", { name: /confirm|delete records/i });
    await expect(confirm).toBeVisible();

    const confirmButton = confirm.getByRole("button", { name: /confirm|delete/i });
    await expect(confirmButton).toBeDisabled();

    const phraseInput = confirm.getByRole("textbox");
    await phraseInput.fill("not-the-topic");
    await expect(confirmButton).toBeDisabled();
    await phraseInput.fill(TOPIC);
    await expect(confirmButton).toBeEnabled();

    await page.keyboard.press("Escape");
    await expect(confirm).toBeHidden();
  });
});
