import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-walk-target";

// Fixture coupling: seed.sh seeds exactly 12 messages for e2e-walk-target,
// spread across days -6, -4 (x2), -2 (x3), -1 (x6) relative to seed time.
// The route's default preset is "7d" (daily slots), which always yields
// exactly 7 slots for that range (see presetToMs/presetToSlotMs in
// clusters.$cluster.topics.$topic.timeline.tsx). If either the fixture
// counts in seed.sh or the default preset/slot width change, the
// expectations below need to be updated together.
const FIXTURE_TOTAL_MESSAGES = 12;
const FIXTURE_SLOT_COUNT = 7;
// seed.sh distributes messages as 1/2/3/6 across the four non-empty days;
// 6 is the largest single-slot bucket and always gets a label since the 7d
// preset (7 bars) is below the labelStep thinning threshold (every bar is
// labeled), and empty slots never get a label.
const FIXTURE_LARGEST_SLOT_COUNT = 6;
const FIXTURE_NON_EMPTY_SLOT_COUNT = 4;

test.describe("Message timeline (Phase 3)", () => {
  test("shows traffic spread across multiple days", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/timeline`);

    await expect(page.getByRole("heading", { level: 1, name: TOPIC })).toBeVisible();
    await expect(page.getByRole("img", { name: /message count per time slot/i })).toBeVisible();
    await expect(page.getByText(new RegExp(`approx total:\\s*${FIXTURE_TOTAL_MESSAGES}`, "i"))).toBeVisible();

    const barHeights = await page
      .getByTestId("timeline-bar")
      .evaluateAll((nodes) => nodes.map((node) => Number(node.getAttribute("height") ?? "0")));

    expect(barHeights).toHaveLength(FIXTURE_SLOT_COUNT);
    expect(new Set(barHeights).size).toBeGreaterThan(2);
    expect(Math.max(...barHeights)).toBeGreaterThan(Math.min(...barHeights));
  });

  test("labels each non-empty bar with its message count", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/timeline`);

    const countLabels = page.getByTestId("timeline-bar-count");
    await expect(countLabels).toHaveCount(FIXTURE_NON_EMPTY_SLOT_COUNT);
    await expect(countLabels.filter({ hasText: new RegExp(`^${FIXTURE_LARGEST_SLOT_COUNT}$`) })).toBeVisible();
  });
});
