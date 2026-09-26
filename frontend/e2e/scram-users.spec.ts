import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";

// Creates, rotates and deletes its own SCRAM user.
test.describe("SCRAM user lifecycle", () => {
  test("creates a user, rotates the password and deletes the credential", async ({ page }) => {
    const user = `e2e-scram-${Date.now()}`;

    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/security/users`);
    await page.getByRole("button", { name: "+ New User" }).click();

    const create = page.getByRole("dialog", { name: "Create / Update SCRAM User" });
    await expect(create).toBeVisible();
    await create.getByPlaceholder("alice").fill(user);
    await create.getByPlaceholder("at least 1 character").fill("e2e-password-1");
    await create.getByRole("button", { name: "Create", exact: true }).click();
    await expect(create).toBeHidden();
    await expect(
      page.getByText(`SCRAM credential set: ${user} / SCRAM-SHA-256 (8192 it.)`),
    ).toBeVisible();

    const row = page.getByRole("row", { name: new RegExp(user) });
    await expect(row).toBeVisible();
    await expect(row).toContainText("SCRAM-SHA-256");
    await expect(row).toContainText("8192");

    // Upsert the existing credential with new iterations.
    await row.getByRole("button", { name: "Rotate password" }).click();
    const rotate = page.getByRole("dialog", { name: "Rotate password" });
    await expect(rotate).toBeVisible();
    await rotate.getByPlaceholder("at least 1 character").fill("e2e-password-2");
    await rotate.getByRole("spinbutton").fill("4096");
    await rotate.getByRole("button", { name: "Rotate", exact: true }).click();
    await expect(rotate).toBeHidden();
    await expect(row).toContainText("4096");

    await row.getByRole("button", { name: "Delete" }).click();
    const confirm = page.getByRole("dialog", { name: "Delete SCRAM credential?" });
    await expect(confirm).toBeVisible();
    await confirm.getByRole("button", { name: "Delete credential" }).click();
    await expect(page.getByText(`Credential deleted: ${user} / SCRAM-SHA-256`)).toBeVisible();
    await expect(row).toBeHidden();
  });
});
