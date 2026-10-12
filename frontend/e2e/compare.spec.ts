import { test, expect, type Page } from "@playwright/test";

// Walks message compare: "Select for compare" on one message and "Compare
// with selected" on another, the compare bar (enter message B by hand,
// clear), Close back to where compare was opened, the verdicts, the exact
// and semantic views, a missing offset and a pasted fingerprint. Read-only.
//
// Fixture coupling (seed.sh): e2e-copy-source holds exactly 1500 records
// "seed-message-1" … "seed-message-1500" at offsets 0 … 1499 on one
// partition. The JSON pair for the semantic verdicts is served by
// page.route, because no fixture topic holds two records that differ only
// in key order.
const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const SOURCE = "e2e-copy-source";
const c = encodeURIComponent(CLUSTER);

const ref = (topic: string, offset: number) => `${CLUSTER}/${topic}/0/${offset}`;
const comparePath = (a: string, b?: string) =>
  `/compare?${new URLSearchParams(b ? { a, b } : { a }).toString()}`;
const summary = (page: Page) => page.getByTestId("compare-summary");
const verdict = (page: Page) => summary(page).getByTestId("verdict");

const listPath = `/clusters/${c}/topics/${SOURCE}/messages?from=start&limit=5`;
const tray = (page: Page) =>
  page.getByRole("complementary", { name: "Message selected for compare" });

/** Expands the n-th message row of the list and returns it. */
async function openRow(page: Page, n: number) {
  const row = page.getByTestId("message-row").nth(n);
  await row.getByRole("button").first().click();
  return row;
}

test("selects a message, compares another one with it and closes", async ({ page }) => {
  await page.goto(listPath);
  const first = await openRow(page, 0);
  await first.getByRole("button", { name: "Select for compare" }).click();
  await expect(first.getByText("Selected for compare")).toBeVisible();
  // The list's sort order decides which offset the first row holds.
  await expect(tray(page)).toContainText(`${CLUSTER} / ${SOURCE} · p0 · #`);

  const second = await openRow(page, 1);
  await second.getByRole("button", { name: "Compare with selected" }).click();
  await expect(page).toHaveURL(/\/compare\?/);
  await expect(page.getByRole("heading", { level: 1, name: "Compare messages" })).toBeVisible();
  await expect(verdict(page)).toHaveText("Different");
  const diff = page.getByRole("table", { name: /differences in the value/i });
  await expect(diff.getByText("Removed")).toHaveCount(1);
  await expect(diff.getByText("Added")).toHaveCount(1);
  await expect(tray(page)).toHaveCount(0);

  // A second compare adds a history entry; Close still returns to the list.
  await page.getByLabel("Ignore JSON array order").check();
  const sideB = page.getByRole("group", { name: "Message B" });
  await sideB.getByLabel("Offset").fill("2");
  await page.getByRole("button", { name: "Compare", exact: true }).click();
  await expect(page).toHaveURL(/b=.*%2F2/);
  await page.getByRole("button", { name: "Close" }).click();
  await expect(page).toHaveURL(new RegExp(`/topics/${SOURCE}/messages\\?.*from=start`));
  // The selection outlives the compare page.
  await expect(tray(page)).toBeVisible();
});

test("enters message B by hand from the compare bar", async ({ page }) => {
  await page.goto(listPath);
  const first = await openRow(page, 0);
  await first.getByRole("button", { name: "Select for compare" }).click();
  await tray(page).getByRole("button", { name: "Enter message B" }).click();

  const sideB = page.getByRole("group", { name: "Message B" });
  // B starts on A's cluster and topic; only partition and offset are missing.
  await expect(sideB.getByLabel("Topic")).toHaveValue(SOURCE);
  await sideB.getByLabel("Partition").fill("0");
  await sideB.getByLabel("Offset").fill("1");
  await page.getByRole("button", { name: "Compare", exact: true }).click();
  await expect(verdict(page)).toHaveText("Different");
});

test("clears the selection from the compare bar", async ({ page }) => {
  await page.goto(listPath);
  const first = await openRow(page, 0);
  await first.getByRole("button", { name: "Select for compare" }).click();
  await tray(page).getByRole("button", { name: "Clear" }).click();
  await expect(tray(page)).toHaveCount(0);
  await expect(first.getByRole("button", { name: "Select for compare" })).toBeVisible();
});

test("closes a compare opened from a link to message A's topic", async ({ page }) => {
  await page.goto(comparePath(ref(SOURCE, 3), ref(SOURCE, 4)));
  await expect(verdict(page)).toHaveText("Different");
  await expect(page.getByRole("link", { name: "Open in topic" })).toHaveCount(2);
  await page.getByRole("button", { name: "Close" }).click();
  await expect(page).toHaveURL(
    new RegExp(`/topics/${SOURCE}/messages\\?.*from=offset.*msgOffset=3`),
  );
});

test("calls a record identical to itself", async ({ page }) => {
  await page.goto(comparePath(ref(SOURCE, 3), ref(SOURCE, 3)));
  await expect(verdict(page)).toHaveText("Identical");
  await expect(summary(page)).toContainText(
    "Key: Identical · Value: Identical · Headers: Identical",
  );
});

test("hides unchanged rows with Only differences", async ({ page }) => {
  await page.goto(comparePath(ref(SOURCE, 0), ref(SOURCE, 1)));
  await expect(verdict(page)).toHaveText("Different");
  await expect(page.getByRole("cell", { name: "Key encoding" })).toBeVisible();

  await page.getByLabel("Only differences").check();
  await expect(page).toHaveURL(/onlyDiff=true/);
  await expect(page.getByRole("cell", { name: "Key encoding" })).toHaveCount(0);
  await expect(page.getByRole("cell", { name: "Reference" })).toBeVisible();
});

test("names an offset without a record and offers none past the end", async ({ page }) => {
  await page.goto(comparePath(ref(SOURCE, 0), ref(SOURCE, 999_999)));
  await expect(page.getByText(`No record at offset`)).toBeVisible();
  await expect(
    page.getByText("The partition holds no record at or after this offset."),
  ).toBeVisible();
});

test("checks message A against its own pasted fingerprint", async ({ page }) => {
  await page.goto(comparePath(ref(SOURCE, 0), ref(SOURCE, 1)));
  const fingerprint = page.getByTestId("fingerprint").first();
  await expect(fingerprint).toContainText("kfp1:");
  const text = (await fingerprint.textContent())?.match(/kfp1:[a-z]+:[0-9a-f]{64}/)?.[0];
  expect(text).toBeTruthy();

  const sideB = page.getByRole("group", { name: "Message B" });
  await sideB.getByLabel("Reference").fill(text ?? "");
  await expect(sideB.getByText("Message A is checked against this fingerprint.")).toBeVisible();
  await page.getByRole("button", { name: "Compare", exact: true }).click();
  await expect(summary(page)).toContainText("Message A has the pasted fingerprint.");
});

test.describe("semantic compare of JSON", () => {
  const A = "e2e-compare-json-a";
  const B = "e2e-compare-json-b";
  const record = (offset: number, value: string) => ({
    partition: 0,
    offset,
    timestamp_ms: 1_700_000_000_000,
    key_encoding: "text",
    key: "order-1",
    value_encoding: "json",
    value,
    value_size_bytes: value.length,
  });

  test.beforeEach(async ({ page }) => {
    const values: Record<string, string> = {
      [A]: '{"id":1,"items":["x","y"],"total":12.5}',
      [B]: '{ "total": 12.50, "items": ["y", "x"], "id": 1 }',
    };
    await page.route(
      (url) =>
        /\/api\/v1\/clusters\/[^/]+\/topics\/e2e-compare-json-[ab]\/messages$/.test(url.pathname),
      async (route) => {
        const topic = new URL(route.request().url()).pathname.split("/")[6];
        await route.fulfill({
          json: { cluster: CLUSTER, topic, messages: [record(0, values[topic])], has_more: false },
        });
      },
    );
  });

  test("ignores key order and number notation, and array order on request", async ({ page }) => {
    await page.goto(comparePath(ref(A, 0), ref(B, 0)));
    await expect(verdict(page)).toHaveText("Different");
    await expect(page.getByText(/Only the order of array elements differs/)).toBeVisible();

    await page.getByLabel("Ignore JSON array order").check();
    await expect(verdict(page)).toHaveText("Equivalent");
    await expect(page).toHaveURL(/anyArrayOrder=true/);

    // The radios are visually hidden; the label is what a user clicks.
    await page.getByRole("group", { name: "Diff view" }).getByText("Exact").click();
    await expect(page.getByRole("radio", { name: "Exact" })).toBeChecked();
    await expect(page).toHaveURL(/view=exact/);
    // The exact view diffs the stored text; the verdict stays semantic.
    await expect(page.getByRole("table", { name: /differences in the value/i })).toBeVisible();
    await expect(verdict(page)).toHaveText("Equivalent");
  });
});
