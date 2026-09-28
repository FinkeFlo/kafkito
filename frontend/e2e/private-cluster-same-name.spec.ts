import { test, expect, type Page } from "@playwright/test";
import { hostAddress } from "./fixtures/host-address";

/**
 * A private (browser-stored) cluster may carry the same name as a shared
 * cluster from the server config. While it exists, requests for that name go
 * to the private cluster. The two must never read each other's cache: the
 * query client keeps entries fresh for 30 s, so a shared cache key would
 * show the other cluster's topics without asking the backend.
 *
 * The private cluster points at the fixture broker too; its topic list is
 * rewritten in flight so the two clusters are told apart on screen.
 *
 * Uses the plain Playwright `test`: the private-cluster fixture seeds a
 * cluster of its own.
 */

const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const SHARED_TOPIC = "e2e-walk-target";
const PRIVATE_TOPIC = "e2e-private-only";

function topicLink(page: Page, name: string) {
  return page.getByRole("main").getByRole("link", { name, exact: true });
}

async function openManageClusters(page: Page) {
  await page.getByRole("button", { name: /^Cluster: / }).click();
  await page.getByRole("link", { name: "Manage clusters…" }).click();
  await expect(page.getByRole("heading", { name: "Private clusters", exact: true })).toBeVisible();
}

async function openTopics(page: Page) {
  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: /^Topics/ })
    .click();
  await expect(page).toHaveURL(new RegExp(`/clusters/${encodeURIComponent(PRIMARY)}/topics$`));
}

test("a private cluster named like a shared one shows its own topics, and back", async ({
  page,
}) => {
  let privateTopicRequests = 0;
  page.on("request", (req) => {
    if (
      new URL(req.url()).pathname === "/api/v1/clusters/__private__/topics" &&
      req.headers()["x-kafkito-cluster"]
    )
      privateTopicRequests++;
  });
  await page.route(
    (url) => url.pathname === "/api/v1/clusters/__private__/topics",
    async (route) => {
      const res = await route.fetch();
      // The first request to a fresh private cluster can fail upstream; pass
      // errors through so the app's retry fetches the list again.
      if (!res.ok()) return route.fulfill({ response: res });
      const body = (await res.json()) as { topics: { name: string }[] };
      const topics = body.topics
        .filter((t) => t.name === SHARED_TOPIC)
        .map((t) => ({ ...t, name: PRIVATE_TOPIC }));
      await route.fulfill({ response: res, json: { ...body, topics } });
    },
  );

  // The shared cluster's topics land in the cache.
  await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);
  await expect(topicLink(page, SHARED_TOPIC)).toBeVisible();

  // Add a private cluster with the same name; every step is an in-app
  // navigation so the cache survives.
  await openManageClusters(page);
  await page.getByRole("button", { name: "Add cluster" }).click();
  const form = page.getByRole("dialog", { name: "Add private cluster" });
  await form.getByPlaceholder("my-dev-cluster").fill(PRIMARY);
  await form.getByPlaceholder("host1:9092, host2:9092").fill(`${hostAddress()}:39092`);
  await form.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(`Saved "${PRIMARY}"`)).toBeVisible();

  await openTopics(page);
  await expect(topicLink(page, PRIVATE_TOPIC)).toBeVisible({ timeout: 15_000 });
  await expect(topicLink(page, SHARED_TOPIC)).toHaveCount(0);
  expect(privateTopicRequests).toBeGreaterThan(0);

  // Remove it again: the name means the shared cluster once more.
  await openManageClusters(page);
  await page
    .getByRole("row", { name: new RegExp(`Select ${PRIMARY} for export`) })
    .getByRole("button", { name: "Delete" })
    .click();
  await page
    .getByRole("dialog", { name: "Delete private cluster?" })
    .getByRole("button", { name: "Delete" })
    .click();
  await expect(
    page.getByRole("row", { name: new RegExp(`Select ${PRIMARY} for export`) }),
  ).toHaveCount(0);

  await openTopics(page);
  await expect(topicLink(page, SHARED_TOPIC)).toBeVisible();
  await expect(topicLink(page, PRIVATE_TOPIC)).toHaveCount(0);
});
