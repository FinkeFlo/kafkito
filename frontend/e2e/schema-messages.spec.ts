import { readFile } from "node:fs/promises";
import { test, expect, type Page } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const c = encodeURIComponent(CLUSTER);

// Fixture coupling: seed.sh's seed_schema_registry produces three Avro
// records in the Confluent wire format to e2e-avro-orders: offset 0 with
// e2e-avro-orders-value v1, offsets 1 and 2 with v2. Offset 2 carries a
// ~100 KB note, so its decoded JSON is cut to the 64 KB list preview.
const TOPIC = "e2e-avro-orders";
const SUBJECT = "e2e-avro-orders-value";

async function schemaId(page: Page, version: number): Promise<number> {
  const res = await page.request.get(
    `/api/v1/clusters/${c}/schemas/subjects/${SUBJECT}/versions/${version}`,
  );
  expect(res.ok()).toBe(true);
  return ((await res.json()) as { id: number }).id;
}

test.describe("Messages of a Schema Registry topic", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto(`/clusters/${c}/topics/${TOPIC}/messages`);
    await expect(page.getByTestId("message-row")).toHaveCount(3);
  });

  test("Avro records are listed decoded, with the encoding and subject badges", async ({
    page,
  }) => {
    const v1Id = await schemaId(page, 1);
    const v2Id = await schemaId(page, 2);

    const first = page.getByTestId("message-row").filter({ hasText: "E2E-AVRO-1" });
    await expect(first.getByText("avro", { exact: true })).toBeVisible();
    await expect(first.getByText(`sr · ${SUBJECT}`, { exact: true })).toBeVisible();
    await expect(first).toContainText('{"amount_cents":1999,"order_id":"E2E-AVRO-1"}');

    const second = page.getByTestId("message-row").filter({ hasText: "E2E-AVRO-2" });
    await expect(second.getByText("avro", { exact: true })).toBeVisible();
    await expect(second).toContainText(
      '{"amount_cents":4250,"currency":"USD","note":"second-order","order_id":"E2E-AVRO-2"}',
    );

    // The raw wire bytes (magic byte, schema id) never reach the list.
    await expect(page.getByText(/^0x00/)).toHaveCount(0);

    await first.getByRole("button").first().click();
    await expect(first.getByText(`value · avro · sr id ${v1Id}`)).toBeVisible();
    await expect(first.locator("pre").last()).toHaveText(
      '{"amount_cents":1999,"order_id":"E2E-AVRO-1"}',
    );
    // A small decoded record needs no full-value actions.
    await expect(first.getByRole("button", { name: /Load full value/ })).toHaveCount(0);
    await expect(first.getByRole("button", { name: "Download full value" })).toHaveCount(0);

    await second.getByRole("button").first().click();
    await expect(second.getByText(`value · avro · sr id ${v2Id}`)).toBeVisible();
  });

  test("a truncated Avro record loads and downloads its decoded JSON in full", async ({ page }) => {
    const v2Id = await schemaId(page, 2);

    const large = page.getByTestId("message-row").filter({ hasText: "preview" });
    await expect(large).toHaveCount(1);
    await expect(large.getByText("avro", { exact: true })).toBeVisible();
    await expect(large).toContainText('{"amount_cents":500,"currency":"EUR","note":"yyy');

    await large.getByRole("button").first().click();
    await expect(
      large.getByText(/^value · avro · sr id \d+ · preview only — full size/),
    ).toBeVisible();

    // The raw-value endpoint serves the decoded JSON (#87), so the preview
    // can be completed for click-to-filter.
    const loadButton = large.getByRole("button", {
      name: "Load full value to enable click-to-filter",
      exact: true,
    });
    await expect(loadButton).toBeVisible();
    await loadButton.click();
    await expect(large.getByText("click to filter")).toBeVisible();
    await expect(large.getByRole("button", { name: '"E2E-AVRO-3"', exact: true })).toBeVisible();

    const downloadPromise = page.waitForEvent("download");
    await large.getByRole("button", { name: "Download full value", exact: true }).click();
    const download = await downloadPromise;

    expect(download.suggestedFilename()).toBe(`${TOPIC}-p0-o2.json`);
    const decoded = JSON.parse(await readFile(await download.path(), "utf8")) as {
      order_id: string;
      amount_cents: number;
      currency: string;
      note: string;
    };
    expect(decoded.order_id).toBe("E2E-AVRO-3");
    expect(decoded.amount_cents).toBe(500);
    expect(decoded.currency).toBe("EUR");
    expect(decoded.note).toHaveLength(100_000);

    // decoded=false still serves the Confluent wire format: magic byte 0x00,
    // big-endian schema id, Avro body.
    const wire = await page.request.get(
      `/api/v1/clusters/${c}/topics/${TOPIC}/messages/0/2/raw?decoded=false`,
    );
    expect(wire.ok()).toBe(true);
    expect(wire.headers()["content-type"]).toBe("application/octet-stream");
    expect(wire.headers()["x-kafkito-value-decoded"]).toBeUndefined();
    const body = await wire.body();
    expect(body[0]).toBe(0);
    expect(body.readUInt32BE(1)).toBe(v2Id);
    expect(body.subarray(5).toString("latin1")).toContain("E2E-AVRO-3");
    expect(body.toString("latin1")).not.toContain('"order_id"');
  });
});
