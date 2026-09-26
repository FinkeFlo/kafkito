import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-walk-target";

// Creates its own consumer group, so the walk really commits: create,
// list, detail, reset offsets and delete touch only that group.
test.describe("Consumer group lifecycle", () => {
  test("creates a group, resets its offsets and deletes it", async ({ page }) => {
    const group = `e2e-walk-group-${Date.now()}`;

    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/consumers`,
    );
    await page.getByRole("button", { name: "Create consumer group" }).click();

    const create = page.getByRole("dialog", { name: /create consumer group/i });
    await expect(create).toBeVisible();
    await create.getByRole("textbox", { name: "Group name" }).fill(group);
    await create.getByRole("combobox", { name: "Strategy" }).selectOption("earliest");
    await create.getByRole("button", { name: "Preview" }).click();
    await expect(create.getByText("p0", { exact: true })).toBeVisible();
    await create.getByRole("button", { name: "Create", exact: true }).click();
    await expect(create).toBeHidden();

    // List, then the detail panel of the new group.
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/groups`);
    const groupTrigger = page.getByRole("button", { name: new RegExp(group) });
    await expect(groupTrigger).toBeVisible();
    await groupTrigger.click();
    await expect(page).toHaveURL(new RegExp(`group=${group}`));
    await expect(page.getByText("Offsets", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: TOPIC, exact: true }).first()).toBeVisible();

    // Reset to latest and commit.
    await page.getByRole("button", { name: /^reset offsets/i }).click();
    const modal = page.getByRole("dialog", { name: /reset offsets/i });
    await expect(modal).toBeVisible();
    await modal.getByRole("combobox", { name: "Strategy" }).selectOption("latest");
    await modal.getByRole("button", { name: /commit reset/i }).click();
    const confirm = page.getByRole("dialog", { name: /commit new offsets/i });
    await expect(confirm).toBeVisible();
    await confirm.getByRole("textbox").fill(group);
    await confirm.getByRole("button", { name: /commit reset/i }).click();
    await expect(modal.getByText("Committed", { exact: true })).toBeVisible();
    await modal.getByRole("button", { name: "Close" }).first().click();
    await expect(modal).toBeHidden();

    // Delete the group.
    await page.getByRole("button", { name: "Delete group" }).click();
    const del = page.getByRole("dialog", { name: new RegExp(`Delete consumer group "${group}"`) });
    await expect(del).toBeVisible();
    await del.getByRole("textbox").fill(group);
    await del.getByRole("button", { name: "Delete group" }).click();
    await expect(del).toBeHidden();
    await expect(page.getByRole("button", { name: new RegExp(group) })).toBeHidden();
  });
});
