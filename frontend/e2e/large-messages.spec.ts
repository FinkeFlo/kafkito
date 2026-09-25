import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-large-message";

// Fixture coupling: seed.sh's produce_large_json puts exactly one record on
// e2e-large-message, ~100 KB, well past consumer.go's 64 KB truncation
// boundary (maxMessageValueBytes). The value is
// `{"_padding":"y"x100000,"order":{...}}` — `_padding` comes first so every
// field under `order` used below is guaranteed to start past byte 64K, i.e.
// inside the part of the value the backend truncates away from the
// list/search preview. produce_large_xml puts an analogous single ~100 KB
// XML record on e2e-large-message-xml:
// `<root><_padding>y…</_padding><order id=… status="shipped">…</order></root>`,
// again with `_padding` first so `order` and everything under it is past the
// boundary. If seed.sh's payload shape changes, the assertions below need to
// move with it.
const NEEDLE_SKU = "E2E-NEEDLE-SKU";
const NEEDLE_TEXT = "e2e-search-needle";
const XML_TOPIC = "e2e-large-message-xml";
const ROOT_ARRAY_TOPIC = "e2e-root-array";

test.describe("Large messages (truncation-tolerant search & click-to-filter)", () => {
  test("text-contains search finds a needle past the 64 KB truncation boundary", async ({
    page,
  }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`,
    );

    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.getByLabel("Value", { exact: true }).fill(NEEDLE_TEXT);
    await page.getByRole("button", { name: "Search", exact: true }).click();

    // stats.matched === 1: search.go's SearchMessages scans the full record
    // (not the 64 KB list preview), so it finds the needle even though it
    // sits past the truncation boundary. The needle itself is never
    // visible in the row's (still truncated) preview text — that's exactly
    // the point: the match count proves the backend searched past what the
    // UI shows, since this topic holds only this one record.
    await expect(page.getByTestId("messages-count")).toHaveText("1");
    await expect(page.getByTestId("message-row")).toBeVisible();
  });

  test("truncated-but-JSON message is badged json, not text", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`,
    );

    const row = page.getByTestId("message-row");
    // consumer.go's decodeBytes sniffs only the first non-whitespace byte
    // when a value is truncated, so a value that is genuinely JSON (but cut
    // off mid-structure by the 64 KB preview cap) still reports
    // value_encoding "json" instead of falling back to "text".
    await expect(row.getByText("json", { exact: true })).toBeVisible();
    await expect(row.getByText("preview", { exact: true })).toBeVisible();
  });

  test("truncated-but-XML message is badged xml, not text", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(XML_TOPIC)}/messages`,
    );

    const row = page.getByTestId("message-row");
    // Same truncation-tolerant heuristic as JSON, but for looksXML (first
    // non-whitespace byte is '<'): consumer.go's decodeBytes reports
    // value_encoding "xml" for a truncated-but-genuinely-XML value instead
    // of falling back to "text".
    await expect(row.getByText("xml", { exact: true })).toBeVisible();
    await expect(row.getByText("preview", { exact: true })).toBeVisible();
  });

  test("click-to-filter: load full value, click a needle in an array, path is wildcarded", async ({
    page,
  }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`,
    );

    await page.getByTestId("message-row").click();

    const loadButton = page.getByRole("button", {
      name: "Load full value to enable click-to-filter",
      exact: true,
    });
    await expect(loadButton).toBeVisible();
    await loadButton.click();

    const needle = page.getByRole("button", { name: `"${NEEDLE_SKU}"`, exact: true });
    await expect(needle).toBeVisible();
    await needle.click();

    // handlePick always wildcards array indices (no "this index vs. all
    // entries" popover — removed per product decision: arrays vary in
    // length/order between messages, so a fixed index is rarely useful).
    const pathInput = page.getByPlaceholder("Type or ↓ for top fields");
    await expect(pathInput).toHaveValue("$.order.items[*].sku");
    await expect(page.getByLabel("Operator", { exact: true })).toHaveValue("eq");
    await expect(page.getByLabel("Value", { exact: true })).toHaveValue(NEEDLE_SKU);
  });

  test("PathSense suggests fields hydrated from the full (untruncated) value", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`,
    );

    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.getByLabel("Mode", { exact: true }).selectOption("jsonpath");

    const pathInput = page.getByPlaceholder("Type or ↓ for top fields");
    // "email" only exists under order.customer.email, past the 64 KB
    // truncation boundary — PathSense can only suggest it if the sample
    // query hydrated the full raw value (hydrateTruncatedSampleMessages)
    // instead of parsing the truncated 64 KB preview (which fails to parse
    // as JSON at all and would otherwise yield an empty suggestion tree).
    await pathInput.fill("ema");
    await expect(page.getByText("$.order.customer.email", { exact: true })).toBeVisible();
  });

  test("PathSense suggests paths for a record whose whole value is an array", async ({ page }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(ROOT_ARRAY_TOPIC)}/messages`,
    );

    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.getByLabel("Mode", { exact: true }).selectOption("jsonpath");

    const pathInput = page.getByPlaceholder("Type or ↓ for top fields");
    // A batch of rows per message is a normal Kafka shape, but these samples
    // used to be skipped entirely, so the dropdown showed "Sample isn't JSON"
    // for a perfectly valid JSON value. The `$[*]` prefix is required: on a
    // root-level array the backend matches nothing for `$.RUNID`.
    await pathInput.fill("RUNID");
    await expect(page.getByText("$[*].RUNID", { exact: true })).toBeVisible();

    await pathInput.fill("step");
    await expect(page.getByText("$[*].meta.step", { exact: true })).toBeVisible();

    // Picking it must produce a query the backend actually matches — the
    // whole point of the `$[*]` prefix. The dropdown carries field names
    // only, so the value is typed by the user, not prefilled.
    await pathInput.fill("RUNID");
    await page.getByText("$[*].RUNID", { exact: true }).click();
    await expect(pathInput).toHaveValue("$[*].RUNID");
    await expect(page.getByLabel("Operator", { exact: true })).toHaveValue("eq");
    await expect(page.getByLabel("Value", { exact: true })).toHaveValue("");

    await page.getByLabel("Value", { exact: true }).fill("E2E-RUN-1");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByTestId("messages-count")).toHaveText("1");
  });

  test("XPath PathSense suggests XML element and attribute paths from the hydrated value", async ({
    page,
  }) => {
    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(XML_TOPIC)}/messages`,
    );

    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.getByLabel("Mode", { exact: true }).selectOption("xpath");

    const pathInput = page.getByLabel("Path", { exact: true });
    // Every element below `_padding` starts past the 64 KB truncation
    // boundary, and the truncated preview is cut mid-element so it does not
    // parse as XML at all — seeing these suggestions proves the sample query
    // hydrated the full raw value before building the XPath tree.
    await pathInput.fill("notes");
    await expect(page.getByText("//root/order/notes", { exact: true })).toBeVisible();

    // Attributes surface as `@name` …
    await pathInput.fill("sku");
    await expect(page.getByText("//root/order/items/item/@sku", { exact: true })).toBeVisible();

    // … and picking a scalar prefills the operator, but not the value: the
    // tree carries element and attribute names only.
    await pathInput.fill("status");
    await page.getByText("//root/order/@status", { exact: true }).click();

    await expect(pathInput).toHaveValue("//root/order/@status");
    await expect(page.getByLabel("Operator", { exact: true })).toHaveValue("eq");
    await expect(page.getByLabel("Value", { exact: true })).toHaveValue("");
  });
});
