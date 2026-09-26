import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const FIXTURE_TOPIC = "e2e-walk-target";
const FIXTURE_TOPIC_LARGE = "e2e-walk-large";
const CREATE_DRAFT_NAME = "e2e-create-walk";

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
    await expect(page.getByRole("row", { name: new RegExp(CREATE_DRAFT_NAME) })).toHaveCount(0);
  });

  test("a created topic shows up in the list without a reload", async ({ page }) => {
    const name = `e2e-create-${Date.now()}`;
    const listPath = `/api/v1/clusters/${encodeURIComponent(CLUSTER)}/topics`;
    try {
      await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);
      await expect(page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC) })).toBeVisible();
      let loads = 0;
      page.on("load", () => loads++);

      await page.getByRole("button", { name: /^\+ New topic$/ }).click();
      const dialog = page.getByRole("dialog", { name: /create topic on/i });
      await dialog.getByLabel("Name").fill(name);

      // The list is fresh for 30 s (staleTime) and never polls, so only the
      // mutation's invalidation can refetch it this quickly.
      const created = page.waitForResponse(
        (res) => res.request().method() === "POST" && new URL(res.url()).pathname === listPath,
      );
      const refetch = page.waitForRequest(
        (req) => req.method() === "GET" && new URL(req.url()).pathname === listPath,
      );
      await dialog.getByRole("button", { name: /^create$/i }).click();
      expect((await created).ok()).toBe(true);
      await refetch;

      await expect(dialog).toBeHidden();
      await expect(page.getByRole("row", { name: new RegExp(name) })).toBeVisible();
      expect(loads).toBe(0);
    } finally {
      await page.request.delete(`${listPath}/${encodeURIComponent(name)}`);
    }
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
