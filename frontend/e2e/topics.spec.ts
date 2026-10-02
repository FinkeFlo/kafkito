import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const FIXTURE_TOPIC = "e2e-walk-target";
const FIXTURE_TOPIC_LARGE = "e2e-walk-large";

test.describe("Topics", () => {
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

  test("list page offers no way to create a topic", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);
    await expect(page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC) })).toBeVisible();

    await expect(page.getByRole("button", { name: /new topic/i })).toHaveCount(0);
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });

  test("topic detail loads with KPIs and sub-tab navigation", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(FIXTURE_TOPIC)}`,
    );

    await expect(page.getByRole("heading", { level: 1, name: FIXTURE_TOPIC })).toBeVisible();

    for (const tab of ["Overview", "Messages", "Produce", "Configs", "Consumers", "Schema"]) {
      await expect(page.getByRole("link", { name: tab, exact: true })).toBeVisible();
    }

    for (const label of ["Lag (all groups)", "Avg msg size"]) {
      await expect(page.getByText(label, { exact: true })).toBeVisible();
    }

    await page.getByRole("main").getByRole("link", { name: "Topics", exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/clusters/${CLUSTER}/topics$`));
  });
});
