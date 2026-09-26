import { test, expect, type Page } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-masked";

// Fixture coupling: kafkito-e2e.yaml masks `$.customer.email` on e2e-masked,
// and seed.sh's produce_masked_json puts two records there: a small one
// (order E2E-MASK-1, email hidden-e2e@example.com) and a newer ~100 KB one
// whose email sits past the 64 KB preview boundary, so its row (badged
// "preview") offers the "Download full value" action.
const SMALL_ORDER = "E2E-MASK-1";
const HIDDEN_TEXT = "hidden-e2e";
const MASKED_MESSAGE = "value is masked and cannot be downloaded";

async function openTopic(page: Page) {
  await page.goto(
    `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`,
  );
}

async function search(page: Page, needle: string) {
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByLabel("Value", { exact: true }).fill(needle);
  await page.getByRole("button", { name: "Search", exact: true }).click();
}

test.describe("Data masking", () => {
  test("masked messages are badged and their full value cannot be downloaded", async ({
    page,
  }, testInfo) => {
    await openTopic(page);

    const rows = page.getByTestId("message-row");
    await expect(rows).toHaveCount(2);
    for (const row of await rows.all()) {
      await expect(row.getByText("masked", { exact: true })).toBeVisible();
    }
    await expect(page.getByText("@example.com")).toHaveCount(0);

    const large = rows.filter({ hasText: "preview" });
    await large.click();
    const download = large.getByRole("button", { name: "Download full value", exact: true });
    await expect(download).toBeDisabled();
    await expect(download).toHaveAccessibleDescription("Masked values can't be downloaded.");
    await expect(large.getByText("Masked values can't be downloaded.")).toBeVisible();
    await expect(large.getByText(/not available for masked values/i)).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("masked-download-disabled.png") });

    const small = rows.filter({ hasNotText: "preview" });
    await small.click();
    await expect(small.getByRole("button", { name: "Download full value" })).toHaveCount(0);
  });

  test("search does not find masked text, but finds the visible part", async ({ page }) => {
    await openTopic(page);

    await search(page, HIDDEN_TEXT);
    await expect(page.getByTestId("messages-count")).toHaveText("0");
    await expect(page.getByTestId("message-row")).toHaveCount(0);

    await page.getByLabel("Value", { exact: true }).fill(SMALL_ORDER);
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByTestId("messages-count")).toHaveText("1");
    const hit = page.getByTestId("message-row");
    await expect(hit.getByText("masked", { exact: true })).toBeVisible();
    await expect(hit).not.toContainText(HIDDEN_TEXT);
  });

  test("a download refused as masked shows the server's message", async ({ page }) => {
    // Simulates a list loaded before the masking rule existed: the row does
    // not know it is masked, so it offers the download and the server
    // refuses it with 403 value_masked.
    await page.route(`**/topics/${TOPIC}/messages?*`, async (route) => {
      const res = await route.fetch();
      const body = (await res.json()) as { messages?: { masked?: boolean }[] };
      for (const m of body.messages ?? []) delete m.masked;
      await route.fulfill({ response: res, json: body });
    });
    await openTopic(page);

    const row = page.getByTestId("message-row").filter({ hasText: "preview" });
    await row.click();
    const downloads: string[] = [];
    page.on("download", (d) => downloads.push(d.suggestedFilename()));
    const refused = page.waitForResponse(
      (r) => r.url().endsWith("/raw") && r.request().method() === "GET",
    );
    await row.getByRole("button", { name: "Download full value", exact: true }).click();
    expect((await refused).status()).toBe(403);

    await expect(page.getByText(MASKED_MESSAGE)).toBeVisible();
    expect(downloads).toEqual([]);
  });
});
