import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";

// Creates and deletes its own rule; the fixture broker runs an authorizer
// with User:ANONYMOUS as super user, so the rule grants nothing that matters.
test.describe("ACL lifecycle", () => {
  test("creates a rule, lists it and deletes it", async ({ page }) => {
    const principal = `User:e2e-acl-${Date.now()}`;

    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/security/acls`);
    await page.getByRole("button", { name: "+ New rule" }).click();

    const modal = page.getByRole("dialog", { name: /new acl on/i });
    await expect(modal).toBeVisible();
    await modal.getByRole("textbox").first().fill(principal);
    await modal.getByPlaceholder("my-topic").fill("e2e-walk-target");
    await modal.getByRole("combobox").nth(2).selectOption("DESCRIBE");
    await modal.getByRole("button", { name: "Create ACL" }).click();
    await expect(modal).toBeHidden();
    await expect(page.getByText(`ACL created: ${principal} ALLOW DESCRIBE`)).toBeVisible();

    const row = page.getByRole("row", { name: new RegExp(principal) });
    await expect(row).toBeVisible();
    await expect(row).toContainText("e2e-walk-target");
    await expect(row).toContainText("DESCRIBE");

    await row.getByRole("button", { name: "Delete ACL" }).click();
    const confirm = page.getByRole("dialog", { name: "Delete this ACL?" });
    await expect(confirm).toBeVisible();
    await confirm.getByRole("button", { name: "Delete ACL" }).click();
    await expect(page.getByText(`1 ACL(s) deleted (${principal}`)).toBeVisible();
    await expect(row).toBeHidden();
  });
});
