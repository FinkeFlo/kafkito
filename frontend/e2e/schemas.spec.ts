import { test, expect, SR_CLUSTER_NAME } from "./fixtures/private-cluster";

const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";

test.describe("Schemas tab (Phase 3, capability-driven)", () => {
  test("schemas link shows '(—)' suffix and aria-disabled when active cluster has no SR", async ({
    page,
  }) => {
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);

    const schemasLink = page.getByRole("link", { name: /^Schemas/ });
    await expect(schemasLink).toBeVisible();
    await expect(schemasLink).toContainText(/\(—\)/);
    await expect(schemasLink).toHaveAttribute("aria-disabled", "true");
  });

  test("schemas landing page shows 'Schemas not configured' notice for cluster without SR", async ({
    page,
  }) => {
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/schemas`);

    await expect(page.getByRole("heading", { level: 1, name: "Schemas" })).toBeVisible();
    await expect(page.getByText(/Schemas not configured/i)).toBeVisible();
    await expect(
      page.getByRole("button", { name: /^\+ Register schema$/ }),
    ).toBeDisabled();
  });

  test("schemas link omits '(—)' suffix and is not aria-disabled when active cluster has SR", async ({
    pageWithSRCluster,
  }) => {
    await pageWithSRCluster.goto(`/clusters/${encodeURIComponent(SR_CLUSTER_NAME)}/topics`);

    const schemasLink = pageWithSRCluster.getByRole("link", {
      name: "Schemas",
      exact: true,
    });
    await expect(schemasLink).toBeVisible();
    await expect(schemasLink).not.toHaveAttribute("aria-disabled", "true");
  });

  test("schemas landing page on SR-enabled cluster renders subjects-list filter, not the not-configured notice", async ({
    pageWithSRCluster,
  }) => {
    await pageWithSRCluster.goto(`/clusters/${encodeURIComponent(SR_CLUSTER_NAME)}/schemas`);

    await expect(
      pageWithSRCluster.getByRole("heading", { level: 1, name: "Schemas" }),
    ).toBeVisible();
    await expect(pageWithSRCluster.getByText(/Schemas not configured/i)).toBeHidden();
    await expect(
      pageWithSRCluster.getByRole("textbox", { name: /filter subjects/i }),
    ).toBeVisible();
  });
});
