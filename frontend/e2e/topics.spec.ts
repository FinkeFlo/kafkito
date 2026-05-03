import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const FIXTURE_TOPIC = "e2e-walk-target";
const FIXTURE_TOPIC_LARGE = "e2e-walk-large";
const CREATE_DRAFT_NAME = "e2e-create-walk";

test.describe("Topics (Phase 3)", () => {
  test("list page renders fixture topics with name and partition count", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);

    await expect(page.getByRole("heading", { name: "Topics", level: 1 })).toBeVisible();

    const targetRow = page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC) });
    await expect(targetRow).toBeVisible();
    await expect(targetRow).toContainText("4");

    const largeRow = page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC_LARGE) });
    await expect(largeRow).toBeVisible();
    await expect(largeRow).toContainText("1");
  });

  test("create modal opens, validates name, aborts cleanly without mutation", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);

    await expect(page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC) })).toBeVisible();
    const initialRowCount = await page.getByRole("row").count();

    await page.getByRole("button", { name: /^\+ New topic$/ }).click();

    const dialog = page.getByRole("dialog", { name: /create topic on/i });
    await expect(dialog).toBeVisible();

    const createButton = dialog.getByRole("button", { name: /^create$/i });
    await expect(createButton).toBeDisabled();

    await dialog.getByLabel("Name").fill(CREATE_DRAFT_NAME);
    await expect(createButton).toBeEnabled();

    await dialog.getByRole("button", { name: /^cancel$/i }).click();
    await expect(dialog).toBeHidden();

    await expect(page.getByRole("row")).toHaveCount(initialRowCount);
    await expect(
      page.getByRole("row", { name: new RegExp(CREATE_DRAFT_NAME) }),
    ).toHaveCount(0);
  });

  test("topic detail loads with KPIs and sub-tab navigation", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(FIXTURE_TOPIC)}`,
    );

    await expect(
      page.getByRole("heading", { level: 1, name: FIXTURE_TOPIC }),
    ).toBeVisible();

    for (const tab of ["Overview", "Messages", "Produce", "Configs", "Consumers", "Schema"]) {
      await expect(page.getByRole("link", { name: tab, exact: true })).toBeVisible();
    }

    for (const label of ["Lag (all groups)", "Avg msg size"]) {
      await expect(page.getByText(label, { exact: true })).toBeVisible();
    }

    await page.getByRole("main").getByRole("link", { name: "Topics", exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/clusters/${CLUSTER}/topics$`));
  });

  test("delete-topic modal requires confirm phrase, aborts cleanly without mutation", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(FIXTURE_TOPIC)}`,
    );

    await page.getByRole("button", { name: /^delete topic$/i }).click();

    const confirm = page.getByRole("dialog", {
      name: new RegExp(`delete topic.*${FIXTURE_TOPIC}`, "i"),
    });
    await expect(confirm).toBeVisible();

    const confirmDelete = confirm.getByRole("button", { name: /^delete topic$/i });
    await expect(confirmDelete).toBeDisabled();

    const phraseInput = confirm.getByLabel(/type.*to confirm/i);
    await phraseInput.fill("not-the-topic");
    await expect(confirmDelete).toBeDisabled();
    await phraseInput.fill(FIXTURE_TOPIC);
    await expect(confirmDelete).toBeEnabled();

    await page.keyboard.press("Escape");
    await expect(confirm).toBeHidden();

    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);
    await expect(
      page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC) }),
    ).toBeVisible();
  });
});
