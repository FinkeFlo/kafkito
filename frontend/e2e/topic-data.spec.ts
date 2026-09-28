import { readFile } from "node:fs/promises";
import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";

// Fixture coupling (seed.sh): e2e-copy-source holds exactly 1500 records
// "seed-message-1" … "seed-message-1500" on one partition, e2e-copy-dest
// and e2e-produce-target start empty, and e2e-large-message holds one
// ~100 KB JSON record containing E2E-NEEDLE-SKU. The copy job fetches 500
// records per page, so copying the source takes three pages and reports
// progress between them.
const COPY_SOURCE = "e2e-copy-source";
const COPY_DEST = "e2e-copy-dest";
const COPY_TOTAL = 1500;
const PRODUCE_TOPIC = "e2e-produce-target";
const LARGE_TOPIC = "e2e-large-message";

function topicPath(topic: string, tab: string): string {
  return `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(topic)}/${tab}`;
}

test.describe("Topic data (produce, search, raw download, bulk copy)", () => {
  test("a message produced in the UI shows up in the list and in search", async ({ page }) => {
    // Unique per attempt, so CI retries do not see an earlier attempt's record.
    const needle = `e2e-produced-${Date.now()}`;

    await page.goto(topicPath(PRODUCE_TOPIC, "produce"));
    await page.getByPlaceholder("(empty key)").fill("e2e-key");
    await page.getByPlaceholder('{"hello":"world"}').fill(`{"needle":"${needle}"}`);
    await page.getByRole("button", { name: "Produce", exact: true }).click();
    await expect(page.getByText(/Produced · partition 0 · offset \d+/)).toBeVisible();

    await page.goto(topicPath(PRODUCE_TOPIC, "messages"));
    const row = page.getByTestId("message-row").filter({ hasText: needle });
    await expect(row).toBeVisible();

    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.getByLabel("Value", { exact: true }).fill(needle);
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByTestId("messages-count")).toHaveText("1");
    await expect(page.getByTestId("message-row")).toHaveCount(1);
    await expect(page.getByTestId("message-row")).toContainText(needle);
  });

  test("X-Kafkito-* headers typed in the form are flagged and replaced by kafkito", async ({
    page,
  }) => {
    const needle = `e2e-provenance-${Date.now()}`;

    await page.goto(topicPath(PRODUCE_TOPIC, "produce"));
    await page.getByPlaceholder('{"hello":"world"}').fill(`{"needle":"${needle}"}`);
    const reservedNote = page.getByText(/are reserved: kafkito drops them/);

    await page.getByRole("button", { name: "Add header" }).click();
    await page.getByLabel("Header 1 key").fill("e2e-trace");
    await page.getByLabel("Header 1 value").fill(needle);
    await expect(reservedNote).toBeHidden();

    await page.getByRole("button", { name: "Add header" }).click();
    await page.getByLabel("Header 2 key").fill("x-kafkito-user");
    await page.getByLabel("Header 2 value").fill("e2e-spoofed-user");
    await expect(reservedNote).toBeVisible();

    await page.getByRole("button", { name: "Produce", exact: true }).click();
    await expect(page.getByText(/Produced · partition 0 · offset \d+/)).toBeVisible();

    // kafkito stamps its own X-Kafkito-Source and X-Kafkito-User (the e2e
    // stack's dev identity); the spoofed lower-case key must not reach the
    // record next to them.
    await page.goto(topicPath(PRODUCE_TOPIC, "messages"));
    const row = page.getByTestId("message-row").filter({ hasText: needle });
    await row.getByRole("button").first().click();
    await expect(row.getByText("headers · 3")).toBeVisible();
    await expect(row).toContainText("X-Kafkito-Source: true");
    await expect(row).toContainText(`e2e-trace: ${needle}`);
    await expect(row).toContainText("X-Kafkito-User: ");
    await expect(row).not.toContainText("x-kafkito-user");
    await expect(row).not.toContainText("e2e-spoofed-user");
  });

  test("the full value of a truncated record downloads as a file", async ({ page }) => {
    await page.goto(topicPath(LARGE_TOPIC, "messages"));
    await page.getByTestId("message-row").click();

    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("button", { name: "Download full value", exact: true }).click();
    const download = await downloadPromise;

    // The name comes from the endpoint's Content-Disposition header, the
    // extension from its content sniffing.
    expect(download.suggestedFilename()).toBe(`${LARGE_TOPIC}-p0-o0.json`);
    const body = await readFile(await download.path(), "utf8");
    expect(body.length).toBeGreaterThan(64 * 1024);
    expect(body).toContain("E2E-NEEDLE-SKU");
    expect(() => JSON.parse(body)).not.toThrow();
  });

  test("bulk copy streams progress and lands the records in the destination", async ({ page }) => {
    await page.goto(topicPath(COPY_SOURCE, "messages"));
    await page.getByRole("button", { name: /Copy messages to another cluster/ }).click();
    await page.getByPlaceholder("topic-name").fill(COPY_DEST);
    await page.keyboard.press("Escape");

    // Record every progress count the panel renders. The copy is fast, so
    // polling for an intermediate state could miss it; a MutationObserver
    // sees each render the SSE events cause.
    await page.evaluate(() => {
      const seen: number[] = [];
      (window as unknown as { __copyProgress: number[] }).__copyProgress = seen;
      new MutationObserver(() => {
        const m = document.body.textContent?.match(/Copying… ([\d,]+) messages so far/);
        if (m) {
          const n = Number(m[1].replaceAll(",", ""));
          if (seen[seen.length - 1] !== n) seen.push(n);
        }
      }).observe(document.body, { subtree: true, childList: true, characterData: true });
    });

    const copyResponse = page.waitForResponse(
      (res) =>
        res.request().method() === "POST" &&
        new URL(res.url()).pathname.endsWith(`/topics/${COPY_SOURCE}/copy`),
    );
    await page.getByRole("button", { name: "Start copy" }).click();
    await expect(
      page.getByText(`Done — ${COPY_TOTAL.toLocaleString("en-US")} messages copied`),
    ).toBeVisible({ timeout: 60_000 });

    // The stream opens with a zero-progress event and reports each page
    // before the job finishes: progress rises through intermediate counts.
    // Checked on the SSE body: events that arrive in one network chunk are
    // handled in one task, so React renders only the last of them and the
    // DOM cannot be relied on to show every intermediate count.
    const events = (await (await copyResponse).text())
      .split("\n")
      .filter((line) => line.startsWith("data: "))
      .map((line) => JSON.parse(line.slice(6)) as { copied: number; done?: boolean });
    const streamed = events.map((ev) => ev.copied);
    expect(streamed[0]).toBe(0);
    expect(streamed.filter((n) => n > 0 && n < COPY_TOTAL).length).toBeGreaterThan(0);
    expect([...streamed].sort((a, b) => a - b)).toEqual(streamed);
    expect(events.at(-1)).toMatchObject({ copied: COPY_TOTAL, done: true });

    // Whatever counts the panel did render, it rendered them in order.
    const progress = await page.evaluate(
      () => (window as unknown as { __copyProgress: number[] }).__copyProgress,
    );
    expect([...progress].sort((a, b) => a - b)).toEqual(progress);

    await page.goto(topicPath(COPY_DEST, "messages"));
    await expect(
      page.getByTestId("message-row").filter({ hasText: `seed-message-${COPY_TOTAL}` }),
    ).toBeVisible();
  });
});
