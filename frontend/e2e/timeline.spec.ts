import { expect, test } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-walk-target";

test.describe("Message timeline (Phase 3)", () => {
  test("shows traffic spread across multiple days", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/timeline`);

    await expect(page.getByRole("heading", { level: 1, name: TOPIC })).toBeVisible();
    await expect(page.getByRole("img", { name: /message count per time slot/i })).toBeVisible();
    await expect(page.getByText(/approx total:\s*12/i)).toBeVisible();

    const barHeights = await page
      .locator('svg[aria-label="Message count per time slot"] rect')
      .evaluateAll((nodes) =>
        nodes.map((node) => Number(node.getAttribute("height") ?? "0")),
      );

    expect(barHeights).toHaveLength(7);
    expect(new Set(barHeights).size).toBeGreaterThan(2);
    expect(Math.max(...barHeights)).toBeGreaterThan(Math.min(...barHeights));
  });
});
