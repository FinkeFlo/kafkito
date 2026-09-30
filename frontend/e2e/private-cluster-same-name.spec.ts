import { test, expect, type Page } from "@playwright/test";
import { privateClusterBroker } from "./fixtures/host-address";

/**
 * A private (browser-stored) cluster must not carry the name of a shared
 * cluster from the server config: requests for that name would go to the
 * private cluster and the shared one could not be reached. The settings form
 * refuses such a name; an entry saved before is flagged until it is renamed.
 * After the rename both clusters are reachable, and neither shows the
 * other's cached topics.
 *
 * The private cluster points at the fixture broker too; its topic list is
 * answered by the test so the two clusters are told apart on screen.
 *
 * Uses the plain Playwright `test`: the private-cluster fixture seeds a
 * cluster of its own.
 */

const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const RENAMED = `${PRIMARY}-private`;
const STORAGE_KEY = "kafkito.private-clusters.v1";
const SHARED_TOPIC = "e2e-walk-target";
const PRIVATE_TOPIC = "e2e-private-only";
const HINT = "Same name as a server cluster – rename it to reach both";

function topicLink(page: Page, name: string) {
  return page.getByRole("main").getByRole("link", { name, exact: true });
}

function settingsRow(page: Page, name: string) {
  return page.getByRole("row", { name: new RegExp(`Select ${name} for export`) });
}

async function openManageClusters(page: Page) {
  await page.getByRole("button", { name: /^Cluster: / }).click();
  await page.getByRole("link", { name: "Manage clusters…" }).click();
  await expect(page.getByRole("heading", { name: "Private clusters", exact: true })).toBeVisible();
}

async function openTopics(page: Page, cluster: string) {
  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: /^Topics/ })
    .click();
  await expect(page).toHaveURL(new RegExp(`/clusters/${encodeURIComponent(cluster)}/topics$`));
}

// The private cluster's topic list is answered here, not by the backend: both
// clusters point at the same fixture broker, so only a distinct topic tells
// them apart on screen.
async function answerPrivateTopics(page: Page) {
  await page.route(
    (url) => url.pathname === "/api/v1/clusters/__private__/topics",
    (route) =>
      route.fulfill({
        json: {
          cluster: "private",
          topics: [
            { name: PRIVATE_TOPIC, partitions: 1, replication_factor: 1, is_internal: false },
          ],
        },
      }),
  );
}

test.describe("Private cluster named like a server cluster", () => {
  test("the settings form refuses the name", async ({ page }) => {
    await page.goto("/settings/clusters");
    await page.getByRole("button", { name: "Add cluster" }).click();
    const form = page.getByRole("dialog", { name: "Add private cluster" });
    const name = form.getByPlaceholder("my-dev-cluster");
    await form.getByPlaceholder("host1:9092, host2:9092").fill(privateClusterBroker());
    await name.fill(PRIMARY);

    const error = form.getByText(
      `A server cluster is already named "${PRIMARY}". Choose another name.`,
    );
    await expect(error).toBeVisible();
    await expect(name).toHaveAttribute("aria-invalid", "true");
    await expect(name).toHaveAccessibleDescription(/already named/);
    await expect(form.getByRole("button", { name: "Save" })).toBeDisabled();

    await name.fill(RENAMED);
    await expect(error).toBeHidden();
    await expect(form.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  test("an entry saved before is flagged, and renaming it makes both reachable", async ({
    page,
  }) => {
    const stored = JSON.stringify([
      {
        id: "7d0c9a52-4e1b-4f3a-8c6d-2b5e9f1a0c3d",
        name: PRIMARY,
        brokers: [privateClusterBroker()],
        auth: { type: "none" },
        tls: { enabled: false },
        created_at: 1767261600000,
        updated_at: 1767261600000,
      },
    ]);
    await page.addInitScript(
      ([key, value]) => {
        if (window.localStorage.getItem(key) === null) window.localStorage.setItem(key, value);
      },
      [STORAGE_KEY, stored],
    );
    await answerPrivateTopics(page);

    // While the private entry holds the name, the name reaches it, not the
    // shared cluster.
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);
    await expect(topicLink(page, PRIVATE_TOPIC)).toBeVisible();
    await expect(topicLink(page, SHARED_TOPIC)).toHaveCount(0);

    await openManageClusters(page);
    const row = settingsRow(page, PRIMARY);
    await expect(row.getByText(HINT)).toBeVisible();
    await expect(row.getByRole("img", { name: "Warning" })).toBeVisible();

    await row.getByRole("button", { name: "Edit" }).click();
    const form = page.getByRole("dialog", { name: "Edit private cluster" });
    await expect(form.getByRole("button", { name: "Save" })).toBeDisabled();
    await form.getByPlaceholder("my-dev-cluster").fill(RENAMED);
    await form.getByRole("button", { name: "Save" }).click();
    await expect(settingsRow(page, RENAMED)).toBeVisible();
    await expect(page.getByText(HINT)).toHaveCount(0);

    // The name now reaches the shared cluster, without the private one's
    // cached topics...
    await openTopics(page, PRIMARY);
    await expect(topicLink(page, SHARED_TOPIC)).toBeVisible();
    await expect(topicLink(page, PRIVATE_TOPIC)).toHaveCount(0);

    // ...and the renamed private cluster is listed and reachable too.
    await page.getByRole("button", { name: /^Cluster: / }).click();
    await page
      .getByRole("listbox", { name: /select cluster/i })
      .getByRole("option", { name: new RegExp(`(^|\\s)${RENAMED}\\s`) })
      .click();
    await expect(page).toHaveURL(new RegExp(`/clusters/${RENAMED}/topics$`));
    await expect(topicLink(page, PRIVATE_TOPIC)).toBeVisible();
    await expect(topicLink(page, SHARED_TOPIC)).toHaveCount(0);
  });
});
