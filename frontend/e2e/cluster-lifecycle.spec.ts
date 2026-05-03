import { test, expect, SECOND_CLUSTER_NAME } from "./fixtures/private-cluster";

const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";

test.describe("Cluster lifecycle (Phase 3)", () => {
  test("picker opens, lists configured clusters, closes on Escape with focus restored", async ({
    page,
  }) => {
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);

    const pill = page.getByRole("button", { name: new RegExp(`^Cluster: ${PRIMARY}`) });
    await expect(pill).toBeVisible();
    await pill.click();

    const listbox = page.getByRole("listbox", { name: /select cluster/i });
    await expect(listbox).toBeVisible();

    const options = listbox.getByRole("option");
    await expect(options).toHaveCount(2);
    await expect(options.nth(0)).toContainText(SECOND_CLUSTER_NAME);
    await expect(options.nth(1)).toContainText(PRIMARY);

    await page.keyboard.press("Escape");
    await expect(listbox).toBeHidden();
    await expect(pill).toBeFocused();
  });

  test("selecting a different cluster updates the URL path and the pill label", async ({
    page,
  }) => {
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);

    await page.getByRole("button", { name: new RegExp(`^Cluster: ${PRIMARY}`) }).click();
    await page
      .getByRole("listbox", { name: /select cluster/i })
      .getByRole("option", { name: new RegExp(SECOND_CLUSTER_NAME) })
      .click();

    await expect(page).toHaveURL(new RegExp(`/clusters/${SECOND_CLUSTER_NAME}/topics$`));
    await expect(
      page.getByRole("button", { name: new RegExp(`^Cluster: ${SECOND_CLUSTER_NAME}`) }),
    ).toBeVisible();
  });

  test("switching cluster from /security/users preserves the sub-tab", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/security/users`);

    await page.getByRole("button", { name: new RegExp(`^Cluster: ${PRIMARY}`) }).click();
    await page
      .getByRole("listbox", { name: /select cluster/i })
      .getByRole("option", { name: new RegExp(SECOND_CLUSTER_NAME) })
      .click();

    await expect(page).toHaveURL(
      new RegExp(`/clusters/${SECOND_CLUSTER_NAME}/security/users$`),
    );
  });

  test("switching cluster from a deep topic URL drops the resource id", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(PRIMARY)}/topics/${encodeURIComponent("e2e-walk-target")}`,
    );

    await page.getByRole("button", { name: new RegExp(`^Cluster: ${PRIMARY}`) }).click();
    await page
      .getByRole("listbox", { name: /select cluster/i })
      .getByRole("option", { name: new RegExp(SECOND_CLUSTER_NAME) })
      .click();

    await expect(page).toHaveURL(new RegExp(`/clusters/${SECOND_CLUSTER_NAME}/topics$`));
  });
});
