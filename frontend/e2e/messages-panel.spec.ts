import { test, expect, type Page, type Request, type Route } from "@playwright/test";

// Characterization walks for the topic messages panel: URL search params,
// browse controls, cursor pagination, search form and run lifecycle, empty
// and error states, and the click-to-filter helpers. They pin the panel's
// current behaviour so structural changes to it can be checked against them.
//
// Fixture coupling (seed.sh): e2e-copy-source holds exactly 1500 records
// "seed-message-1" … "seed-message-1500" at offsets 0 … 1499 on one
// partition; e2e-walk-target holds 12 records spread over 4 partitions;
// e2e-root-array holds one JSON record whose value is an array (its first
// element padded past the 64 KB preview, so the row is truncated);
// e2e-masked holds one small, untruncated JSON record.
const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const SOURCE = "e2e-copy-source";
const SPREAD = "e2e-walk-target";
const SPREAD_TOTAL = 12;
const SMALL_JSON = "e2e-masked";
const COACHMARK_KEY = "kafkito.coachmark.livejson.seen";
const COACHMARK_TEXT = "Tip: click any value in a JSON message to filter by it.";

const c = encodeURIComponent(CLUSTER);
const pagePath = (topic: string, query = "") =>
  `/clusters/${c}/topics/${encodeURIComponent(topic)}/messages${query}`;
const listPath = (topic: string) => `/api/v1/clusters/${c}/topics/${topic}/messages`;
const searchPath = (topic: string) => `${listPath(topic)}/search`;

/** Records every message-list GET for `topic` (not count/search/sample). */
function recordListRequests(page: Page, topic: string): URL[] {
  const seen: URL[] = [];
  page.on("request", (req: Request) => {
    const url = new URL(req.url());
    if (req.method() === "GET" && url.pathname === listPath(topic)) seen.push(url);
  });
  return seen;
}

/** Records the JSON body of every search POST for `topic`. */
async function recordSearchBodies(page: Page, topic: string): Promise<Record<string, unknown>[]> {
  const seen: Record<string, unknown>[] = [];
  await page.route(
    (url) => url.pathname === searchPath(topic),
    async (route: Route) => {
      seen.push(route.request().postDataJSON() as Record<string, unknown>);
      await route.fallback();
    },
  );
  return seen;
}

function searchParams(page: Page): URLSearchParams {
  return new URL(page.url()).searchParams;
}

const rows = (page: Page) => page.getByTestId("message-row");
const count = (page: Page) => page.getByTestId("messages-count");

/** Row texts of the rendered list, e.g. ["seed-message-3", …]. */
async function seedValues(page: Page): Promise<string[]> {
  const texts = await rows(page).allInnerTexts();
  return texts.map((t) => t.match(/seed-message-\d+/)?.[0] ?? "");
}

function msg(partition: number, offset: number, value: string) {
  return {
    partition,
    offset,
    timestamp_ms: 1_700_000_000_000 + offset,
    key_encoding: "null",
    value,
    value_encoding: "text",
  };
}

function stats(over: Record<string, unknown>) {
  return {
    scanned: 0,
    matched: 0,
    budget_exhausted: false,
    timed_out: false,
    more_available: false,
    direction: "newest_first",
    parse_errors: 0,
    ...over,
  };
}

async function openSearch(page: Page) {
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByRole("button", { name: "Close search", exact: true })).toBeVisible();
}

async function runSearch(page: Page) {
  await page.getByRole("button", { name: "Search", exact: true }).click();
}

test.describe("Messages panel — URL search params and browse controls", () => {
  test("URL params drive the controls and the first page request", async ({ page }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE, "?partition=0&limit=5&from=offset&msgOffset=10"));

    await expect(page.getByLabel("Partition", { exact: true })).toHaveValue("0");
    await expect(page.getByLabel("From", { exact: true })).toHaveValue("offset");
    await expect(page.getByLabel("Start offset")).toHaveValue("10");
    await expect(page.getByLabel("Limit", { exact: true })).toHaveValue("5");
    await expect(page.getByText("0–1,499", { exact: true })).toBeVisible();
    await expect(page.getByLabel("Start offset")).toHaveAttribute(
      "title",
      "Valid 0–1,499. Press Enter to apply.",
    );

    await expect(count(page)).toHaveText("5");
    expect(await seedValues(page)).toEqual([
      "seed-message-15",
      "seed-message-14",
      "seed-message-13",
      "seed-message-12",
      "seed-message-11",
    ]);

    const first = requests[0];
    expect(first.searchParams.get("partition")).toBe("0");
    expect(first.searchParams.get("limit")).toBe("5");
    expect(first.searchParams.get("from")).toBe("offset");
    expect(first.searchParams.get("offset")).toBe("10");
    expect(first.searchParams.get("partition_offsets")).toBeNull();
    expect(first.searchParams.get("cursor")).toBeNull();
  });

  test("defaults: all partitions, latest, limit 50, newest first", async ({ page }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE));

    await expect(page.getByLabel("Partition", { exact: true })).toHaveValue("-1");
    await expect(page.getByLabel("From", { exact: true })).toHaveValue("end");
    await expect(page.getByLabel("Limit", { exact: true })).toHaveValue("50");
    await expect(page.getByLabel("Sort", { exact: true })).toHaveValue("newest");
    await expect(page.getByLabel("Sort", { exact: true })).toHaveAttribute(
      "title",
      "Order of displayed messages",
    );
    await expect(page.getByLabel("Start offset")).toHaveCount(0);
    await expect(count(page)).toHaveText("50");
    expect((await seedValues(page))[0]).toBe("seed-message-1500");

    const first = requests[0];
    expect(first.searchParams.get("partition")).toBeNull();
    expect(first.searchParams.get("limit")).toBe("50");
    expect(first.searchParams.get("from")).toBe("end");
    expect(first.searchParams.get("offset")).toBeNull();
  });

  test("limit is committed on Enter or blur, clamped to 1–500, and written to the URL", async ({
    page,
  }) => {
    await page.goto(pagePath(SOURCE));
    await expect(count(page)).toHaveText("50");
    const limit = page.getByLabel("Limit", { exact: true });

    await limit.fill("7");
    // Typing alone does not refetch.
    await expect(count(page)).toHaveText("50");
    await limit.press("Enter");
    await expect(count(page)).toHaveText("7");
    expect(searchParams(page).get("limit")).toBe("7");

    await limit.fill("9999");
    await limit.blur();
    await expect(limit).toHaveValue("500");
    await expect.poll(() => searchParams(page).get("limit")).toBe("500");
    await expect(count(page)).toHaveText("500");

    await limit.fill("0");
    await limit.press("Enter");
    await expect(limit).toHaveValue("50");
    await expect.poll(() => searchParams(page).get("limit")).toBe("50");
  });

  test("from=offset seeks, clamps the offset to the partition's range and syncs the URL", async ({
    page,
  }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE, "?limit=3"));
    await expect(count(page)).toHaveText("3");

    await page.getByLabel("From", { exact: true }).selectOption("offset");
    await expect.poll(() => searchParams(page).get("from")).toBe("offset");
    const offset = page.getByLabel("Start offset");
    await expect(offset).toHaveValue("0");
    await expect(page.getByText("all · 0–1,499", { exact: true })).toBeVisible();
    await expect(offset).toHaveAttribute(
      "title",
      "Seeks every partition to this offset (valid 0–1,499). Press Enter to apply.",
    );
    // Partition = all seeks every partition via partition_offsets.
    await expect.poll(() => requests.at(-1)?.searchParams.get("partition_offsets")).toBe("0:0");
    expect(requests.at(-1)?.searchParams.get("offset")).toBeNull();
    expect(await seedValues(page)).toEqual(["seed-message-3", "seed-message-2", "seed-message-1"]);

    await offset.fill("99999");
    await offset.press("Enter");
    await expect(offset).toHaveValue("1499");
    await expect.poll(() => searchParams(page).get("msgOffset")).toBe("1499");
    await expect(count(page)).toHaveText("1");
    expect(await seedValues(page)).toEqual(["seed-message-1500"]);

    await offset.fill("abc");
    await offset.blur();
    await expect(offset).toHaveValue("0");
    await expect.poll(() => searchParams(page).get("msgOffset")).toBe("0");

    await page.getByLabel("From", { exact: true }).selectOption("start");
    await expect.poll(() => searchParams(page).get("from")).toBe("start");
    await expect(offset).toHaveCount(0);
    await expect
      .poll(() => seedValues(page))
      .toEqual(["seed-message-3", "seed-message-2", "seed-message-1"]);
  });

  test("sort order flips the rendered order without refetching", async ({ page }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE, "?limit=3&from=start"));
    await expect(count(page)).toHaveText("3");
    expect(await seedValues(page)).toEqual(["seed-message-3", "seed-message-2", "seed-message-1"]);
    const before = requests.length;

    await page.getByLabel("Sort", { exact: true }).selectOption("oldest");
    await expect
      .poll(() => seedValues(page))
      .toEqual(["seed-message-1", "seed-message-2", "seed-message-3"]);
    expect(requests.length).toBe(before);
    expect(searchParams(page).get("sort")).toBeNull();
  });

  test("partition select filters the list and writes the partition to the URL", async ({
    page,
  }) => {
    await page.goto(pagePath(SPREAD));
    await expect(count(page)).toHaveText(String(SPREAD_TOTAL));
    const select = page.getByLabel("Partition", { exact: true });
    await expect(select.locator("option")).toHaveText(["all", "0", "1", "2", "3"]);

    let total = 0;
    for (const p of [0, 1, 2, 3]) {
      await select.selectOption(String(p));
      await expect.poll(() => searchParams(page).get("partition")).toBe(String(p));
      await expect(count(page)).not.toContainText("fetching");
      const n = Number(await count(page).innerText());
      if (n === 0) {
        await expect(page.getByText("No messages.", { exact: true })).toBeVisible();
      } else {
        await expect(rows(page)).toHaveCount(n);
        for (const text of await rows(page).allInnerTexts()) expect(text).toContain(`p${p}`);
      }
      total += n;
    }
    expect(total).toBe(SPREAD_TOTAL);

    await select.selectOption("-1");
    await expect.poll(() => searchParams(page).get("partition")).toBe("-1");
    await expect(count(page)).toHaveText(String(SPREAD_TOTAL));
  });

  test("Refresh refetches the head page", async ({ page }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE, "?limit=3"));
    await expect(count(page)).toHaveText("3");
    const before = requests.length;
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect.poll(() => requests.length).toBeGreaterThan(before);
  });

  test("a browse time range adds from/to timestamps and labels the picker", async ({ page }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE, "?limit=3"));
    await expect(count(page)).toHaveText("3");

    const picker = page.getByRole("button", { name: /Any time/ });
    await expect(picker).toHaveAttribute("title", "Filter messages by timestamp");
    await picker.click();
    await page
      .getByRole("dialog", { name: "Time range picker" })
      .getByRole("button", { name: "Last 30 days", exact: true })
      .click();
    await expect(page.getByRole("button", { name: /Last 30 days/ })).toBeVisible();

    await expect.poll(() => requests.at(-1)?.searchParams.get("from_ts_ms")).not.toBeNull();
    const last = requests.at(-1) as URL;
    const fromTs = Number(last.searchParams.get("from_ts_ms"));
    const toTs = Number(last.searchParams.get("to_ts_ms"));
    expect(toTs - fromTs).toBe(30 * 24 * 60 * 60_000);
    await expect(count(page)).toHaveText("3");
  });
});

test.describe("Messages panel — cursor pagination", () => {
  test("Load more appends the next page using the cursor, and a filter change resets it", async ({
    page,
  }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE));
    await expect(count(page)).toHaveText("50");

    const loadMore = page.getByRole("button", { name: "Load more", exact: true });
    await expect(loadMore).toBeVisible();
    await loadMore.click();
    await expect(count(page)).toHaveText("100");
    await expect(rows(page)).toHaveCount(100);
    const values = await seedValues(page);
    expect(values[0]).toBe("seed-message-1500");
    expect(values[99]).toBe("seed-message-1401");

    const paged = requests.filter((u) => u.searchParams.get("cursor"));
    expect(paged).toHaveLength(1);
    expect(paged[0].searchParams.get("limit")).toBe("50");
    expect(paged[0].searchParams.get("from")).toBe("end");

    await page.getByLabel("Limit", { exact: true }).fill("10");
    await page.getByLabel("Limit", { exact: true }).press("Enter");
    await expect(count(page)).toHaveText("10");
    await expect(rows(page)).toHaveCount(10);
    await expect(loadMore).toBeVisible();
  });

  test("Live hides Load more; a topic without more pages never shows it", async ({ page }) => {
    await page.goto(pagePath(SOURCE));
    const loadMore = page.getByRole("button", { name: "Load more", exact: true });
    await expect(loadMore).toBeVisible();
    await page.getByLabel("Live").check();
    await expect(loadMore).toHaveCount(0);
    await page.getByLabel("Live").uncheck();
    await expect(loadMore).toBeVisible();

    await page.goto(pagePath(SPREAD));
    await expect(count(page)).toHaveText(String(SPREAD_TOTAL));
    await expect(loadMore).toHaveCount(0);
  });

  test("Live polls the head page", async ({ page }) => {
    const requests = recordListRequests(page, SOURCE);
    await page.goto(pagePath(SOURCE, "?limit=3"));
    await expect(count(page)).toHaveText("3");
    await page.getByLabel("Live").check();
    const before = requests.length;
    await expect.poll(() => requests.length, { timeout: 7_000 }).toBeGreaterThanOrEqual(before + 2);
  });

  test("a failed Load more shows the error and keeps the button", async ({ page }) => {
    await page.route(
      (url) => url.pathname === listPath(SOURCE) && url.searchParams.has("cursor"),
      (route: Route) =>
        route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "e2e-load-more-failed" }),
        }),
    );
    await page.goto(pagePath(SOURCE));
    await expect(count(page)).toHaveText("50");
    const loadMore = page.getByRole("button", { name: "Load more", exact: true });
    await loadMore.click();
    await expect(page.getByText(/e2e-load-more-failed/)).toBeVisible();
    await expect(loadMore).toBeEnabled();
    await expect(count(page)).toHaveText("50");
  });
});

test.describe("Messages panel — error, partial and empty states", () => {
  test("a failing head page shows the error message", async ({ page }) => {
    await page.route(
      (url) => url.pathname === listPath(SOURCE),
      (route: Route) =>
        route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "e2e-list-failed" }),
        }),
    );
    await page.goto(pagePath(SOURCE));
    // The query retries three times with backoff before it reports the error.
    await expect(page.getByText(/e2e-list-failed/)).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText("No messages.", { exact: true })).toBeVisible();
    await expect(rows(page)).toHaveCount(0);
  });

  test("a partial page shows the incomplete-page warning", async ({ page }) => {
    await page.route(
      (url) => url.pathname === listPath(SOURCE),
      (route: Route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            cluster: CLUSTER,
            topic: SOURCE,
            messages: [msg(0, 7, "e2e-partial-row")],
            has_more: false,
            partial: true,
          }),
        }),
    );
    await page.goto(pagePath(SOURCE));
    await expect(rows(page)).toHaveCount(1);
    await expect(page.getByText(/This page may be incomplete/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Load more", exact: true })).toHaveCount(0);
  });
});

test.describe("Messages panel — search form", () => {
  test("the mode select switches the fields and the Search button's guard", async ({ page }) => {
    await page.goto(pagePath(SOURCE, "?limit=3"));
    await expect(count(page)).toHaveText("3");
    await expect(page.getByLabel("Mode")).toHaveCount(0);
    await openSearch(page);

    const mode = page.getByLabel("Mode", { exact: true });
    const run = page.getByRole("button", { name: "Search", exact: true });
    await expect(mode).toHaveValue("contains");
    await expect(mode.locator("option")).toHaveText([
      "Text contains",
      "JSONPath",
      "XPath",
      "JavaScript",
    ]);
    await expect(page.getByLabel("Path", { exact: true })).toHaveCount(0);
    await expect(page.getByLabel("Operator", { exact: true })).toHaveCount(0);
    await expect(page.getByLabel("Value", { exact: true })).toHaveAttribute(
      "placeholder",
      "Substring",
    );
    await expect(run).toBeEnabled();
    await expect(page.getByText("Stop after Limit matches (3)")).toBeVisible();

    await mode.selectOption("jsonpath");
    await expect(page.getByPlaceholder("Type or ↓ for top fields")).toBeVisible();
    const op = page.getByLabel("Operator", { exact: true });
    await expect(op).toHaveValue("contains");
    await expect(op.locator("option")).toHaveText([
      "exists",
      "=",
      "≠",
      "contains",
      "regex",
      ">",
      "≥",
      "<",
      "≤",
    ]);
    await expect(run).toBeDisabled();
    await page.getByPlaceholder("Type or ↓ for top fields").fill("$.a");
    await expect(run).toBeEnabled();
    await op.selectOption("exists");
    await expect(page.getByLabel("Value", { exact: true })).toBeDisabled();
    await expect(page.getByLabel("Value", { exact: true })).toHaveAttribute(
      "placeholder",
      "(ignored)",
    );
    await op.selectOption("eq");
    await expect(page.getByLabel("Value", { exact: true })).toHaveAttribute(
      "placeholder",
      "e.g. 42 / shipped / ^A.*",
    );

    await mode.selectOption("xpath");
    await expect(page.getByPlaceholder("//order/@status")).toBeVisible();

    await mode.selectOption("js");
    await expect(page.getByLabel("Path", { exact: true })).toHaveCount(0);
    const expression = page.getByLabel("Expression", { exact: true });
    await expect(expression).toBeVisible();
    await expect(page.getByText(/Limit 100 ms per message\./)).toBeVisible();
    await expect(run).toBeDisabled();
    await expression.fill("true");
    await expect(run).toBeEnabled();

    await page.getByRole("button", { name: "Close search", exact: true }).click();
    await expect(page.getByLabel("Mode")).toHaveCount(0);
    // State survives closing and reopening the panel.
    await openSearch(page);
    await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("js");
    await expect(page.getByLabel("Expression", { exact: true })).toHaveValue("true");
  });

  test("range, direction and budget controls feed the search request", async ({ page }) => {
    const bodies = await recordSearchBodies(page, SOURCE);
    await page.goto(pagePath(SOURCE, "?limit=3"));
    await expect(count(page)).toHaveText("3");
    await openSearch(page);

    const range = page.getByRole("combobox", { name: "Range", exact: true });
    await expect(range).toHaveValue("off");
    await range.selectOption("custom");
    await expect(page.getByLabel("Search range from")).toBeVisible();
    await expect(page.getByLabel("Search range to")).toBeVisible();
    await range.selectOption("preset");
    const presetLabels = [
      "Last 5m",
      "Last 15m",
      "Last 1h",
      "Last 6h",
      "Last 24h",
      "Last 7d",
      "Last 30d",
      "Today",
      "Yesterday",
    ];
    for (const label of presetLabels) {
      await expect(page.getByRole("button", { name: label, exact: true })).toBeVisible();
    }
    await expect(page.getByRole("button", { name: "Last 24h", exact: true })).toHaveClass(
      /border-accent/,
    );
    await page.getByRole("button", { name: "Last 1h", exact: true }).click();
    await expect(page.getByRole("button", { name: "Last 1h", exact: true })).toHaveClass(
      /border-accent/,
    );

    await page.getByLabel("Direction", { exact: true }).selectOption("oldest_first");
    await page.getByLabel("Max messages to scan").fill("1234");
    await page.getByLabel("Stop after Limit matches (3)").uncheck();
    await page.getByLabel("Value", { exact: true }).fill("no-such-needle");
    await runSearch(page);
    await expect(page.getByText("No matches.", { exact: true })).toBeVisible();

    const body = bodies[0];
    expect(body).toMatchObject({
      partition: -1,
      limit: 3,
      direction: "oldest_first",
      stop_on_limit: false,
      mode: "contains",
      path: "",
      op: "contains",
      value: "no-such-needle",
      zones: ["value", "key", "headers"],
      budget: 1234,
    });
    expect(Number(body.to_ts_ms) - Number(body.from_ts_ms)).toBe(60 * 60_000);
    expect(body.cursors).toBeUndefined();

    await page.getByLabel("Scan whole topic").check();
    await expect(page.getByLabel("Max messages to scan")).toBeDisabled();
    await range.selectOption("off");
    await runSearch(page);
    await expect.poll(() => bodies.length).toBeGreaterThan(1);
    const unlimited = bodies[1];
    expect(unlimited.budget).toBe(1_000_000);
    expect(unlimited.from_ts_ms).toBeUndefined();
    expect(unlimited.to_ts_ms).toBeUndefined();
  });

  test("structured modes send path, operator and value-only zones", async ({ page }) => {
    const bodies = await recordSearchBodies(page, SMALL_JSON);
    await page.goto(pagePath(SMALL_JSON));
    await expect(rows(page)).toHaveCount(2);
    await openSearch(page);
    await page.getByLabel("Mode", { exact: true }).selectOption("jsonpath");
    await page.getByPlaceholder("Type or ↓ for top fields").fill("$.order");
    await page.keyboard.press("Escape");
    await page.getByLabel("Operator", { exact: true }).selectOption("eq");
    await page.getByLabel("Value", { exact: true }).fill("E2E-MASK-1");
    await runSearch(page);
    await expect(count(page)).toHaveText("1");
    expect(bodies[0]).toMatchObject({
      mode: "jsonpath",
      path: "$.order",
      op: "eq",
      value: "E2E-MASK-1",
      zones: ["value"],
      budget: 50000,
      stop_on_limit: true,
      direction: "newest_first",
    });
  });
});

test.describe("Messages panel — search run lifecycle", () => {
  test("a search replaces the list, locks browse controls and Clear restores it", async ({
    page,
  }) => {
    await page.goto(pagePath(SOURCE));
    await expect(count(page)).toHaveText("50");
    await openSearch(page);
    // Matches seed-message-15, -150 … -159 and -1500.
    await page.getByLabel("Value", { exact: true }).fill("seed-message-15");
    await runSearch(page);

    await expect(count(page)).toHaveText("12");
    await expect(rows(page)).toHaveCount(12);
    await expect(page.getByText("12 matches", { exact: true })).toBeVisible();
    await expect(page.getByText("· 1,500 scanned", { exact: true })).toBeVisible();
    await expect(page.getByText("Range fully scanned", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Search more →" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Load more", exact: true })).toHaveCount(0);

    await expect(page.getByLabel("From", { exact: true })).toBeDisabled();
    await expect(page.getByLabel("Live")).toBeDisabled();
    await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeDisabled();
    await expect(page.getByRole("button", { name: /Any time/ })).toBeDisabled();
    await expect(page.getByLabel("Partition", { exact: true })).toBeEnabled();

    await page.getByRole("button", { name: "Clear result", exact: true }).click();
    await expect(count(page)).toHaveText("50");
    await expect(page.getByText("12 matches", { exact: true })).toHaveCount(0);
    await expect(page.getByLabel("From", { exact: true })).toBeEnabled();
    await expect(page.getByRole("button", { name: "Load more", exact: true })).toBeVisible();
  });

  test("the scan budget stops a search and Search more continues it", async ({ page }) => {
    const bodies: Record<string, unknown>[] = [];
    await page.route(
      (url) => url.pathname === searchPath(SOURCE),
      async (route: Route) => {
        bodies.push(route.request().postDataJSON() as Record<string, unknown>);
        const n = bodies.length;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            cluster: CLUSTER,
            topic: SOURCE,
            messages: n === 1 ? [msg(0, 1499, "e2e-budget-hit")] : [],
            search: stats({
              scanned: 100,
              matched: n === 1 ? 1 : 0,
              more_available: true,
              budget_exhausted: true,
              next_cursors: { "0": 1500 - n * 100 },
            }),
          }),
        });
      },
    );
    await page.goto(pagePath(SOURCE));
    await openSearch(page);
    await page.getByLabel("Max messages to scan").fill("100");
    await page.getByLabel("Value", { exact: true }).fill("e2e-budget");
    await runSearch(page);

    await expect(page.getByText("Scan limit reached", { exact: true })).toBeVisible();
    await expect(page.getByText("· 100 scanned", { exact: true })).toBeVisible();
    await expect(page.getByText("1 matches", { exact: true })).toBeVisible();
    expect(bodies).toHaveLength(1);
    expect(bodies[0].budget).toBe(100);

    await page.getByRole("button", { name: "Search more →" }).click();
    await expect(page.getByText("· 200 scanned", { exact: true })).toBeVisible();
    await expect(page.getByText("Scan limit reached", { exact: true })).toBeVisible();
    await expect(page.getByText("1 matches", { exact: true })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    expect(bodies).toHaveLength(2);
    expect(bodies[1].budget).toBe(100);
    expect(bodies[1].cursors).toEqual({ "0": 1400 });
  });

  test("stop-on-limit ends an auto-chained search with Limit reached", async ({ page }) => {
    const bodies: Record<string, unknown>[] = [];
    await page.route(
      (url) => url.pathname === searchPath(SOURCE),
      async (route: Route) => {
        bodies.push(route.request().postDataJSON() as Record<string, unknown>);
        const offset = bodies.length * 10;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            cluster: CLUSTER,
            topic: SOURCE,
            messages: [0, 1, 2].map((i) => msg(0, offset + i, `e2e-limit-${offset + i}`)),
            search: stats({
              scanned: 10,
              matched: 3,
              more_available: true,
              next_cursors: { "0": offset },
            }),
          }),
        });
      },
    );
    await page.goto(pagePath(SOURCE, "?limit=5"));
    await openSearch(page);
    await page.getByLabel("Value", { exact: true }).fill("e2e-limit");
    await runSearch(page);
    await expect(page.getByText("Limit reached", { exact: true })).toBeVisible();
    await expect(page.getByText("6 matches", { exact: true })).toBeVisible();
    await expect(page.getByText("· 20 scanned", { exact: true })).toBeVisible();
    await expect(rows(page)).toHaveCount(6);
    await expect(page.getByRole("button", { name: "Search more →" })).toBeVisible();
    expect(bodies).toHaveLength(2);
    expect(bodies[0].limit).toBe(5);
    expect(bodies[1].cursors).toEqual({ "0": 10 });
  });

  test("an auto-chained search reports progress, continues from cursors and can be stopped", async ({
    page,
  }) => {
    let release: (() => void) | undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const bodies: Record<string, unknown>[] = [];
    await page.route(
      (url) => url.pathname === searchPath(SOURCE),
      async (route: Route) => {
        const body = route.request().postDataJSON() as Record<string, unknown>;
        bodies.push(body);
        if (bodies.length === 1) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              cluster: CLUSTER,
              topic: SOURCE,
              messages: [msg(0, 42, "e2e-chain-hit")],
              search: stats({
                scanned: 10,
                matched: 1,
                more_available: true,
                next_cursors: { "0": 5 },
                parse_errors: 2,
                parse_error_offsets: [
                  { partition: 0, offset: 3, error: "bad json" },
                  { partition: 0, offset: 4, error: "worse json" },
                ],
              }),
            }),
          });
          return;
        }
        await held;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            cluster: CLUSTER,
            topic: SOURCE,
            messages: [],
            search: stats({ scanned: 10, matched: 0, more_available: true }),
          }),
        });
      },
    );

    await page.goto(pagePath(SOURCE));
    await expect(count(page)).toHaveText("50");
    await openSearch(page);
    await page.getByLabel("Value", { exact: true }).fill("e2e-chain");
    await runSearch(page);

    await expect(page.getByRole("button", { name: "Searching…", exact: true })).toBeDisabled();
    await expect(count(page)).toHaveText("1 · 10 scanned · searching…");
    await expect(rows(page)).toHaveCount(1);
    await expect(page.getByText("· 10 scanned …")).toBeVisible();
    const parseErrors = page.getByText("· 2 parse errors skipped");
    await expect(parseErrors).toHaveAttribute("title", "p0@3: bad json\np0@4: worse json");
    await expect(page.getByRole("button", { name: "Clear result", exact: true })).toHaveCount(0);

    await page.getByRole("button", { name: "Stop", exact: true }).click();
    release?.();

    await expect(page.getByText("Stopped", { exact: true })).toBeVisible();
    await expect(count(page)).toHaveText("1");
    await expect(page.getByText("· 20 scanned", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Search more →" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Stop", exact: true })).toHaveCount(0);
    expect(bodies).toHaveLength(2);
    expect(bodies[1].cursors).toEqual({ "0": 5 });
    expect(bodies[1].budget).toBe(50000 - 10);
  });

  test("while a fresh search runs without matches the list says so", async ({ page }) => {
    let release: (() => void) | undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route(
      (url) => url.pathname === searchPath(SOURCE),
      async (route: Route) => {
        await held;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            cluster: CLUSTER,
            topic: SOURCE,
            messages: [],
            search: stats({ scanned: 1500, matched: 0 }),
          }),
        });
      },
    );
    await page.goto(pagePath(SOURCE));
    await expect(count(page)).toHaveText("50");
    await openSearch(page);
    await page.getByLabel("Value", { exact: true }).fill("anything");
    await runSearch(page);

    await expect(page.getByText("Searching… 0 scanned, no match yet.")).toBeVisible();
    await expect(count(page)).toHaveText("0 · 0 scanned · searching…");
    await expect(rows(page)).toHaveCount(0);
    release?.();
    await expect(page.getByText("No matches.", { exact: true })).toBeVisible();
    await expect(count(page)).toHaveText("0");
  });

  test("a failed search shows the server's message and keeps the browse list", async ({ page }) => {
    await page.route(
      (url) => url.pathname === searchPath(SOURCE),
      (route: Route) =>
        route.fulfill({ status: 400, contentType: "text/plain", body: "e2e-bad-search" }),
    );
    await page.goto(pagePath(SOURCE));
    await expect(count(page)).toHaveText("50");
    await openSearch(page);
    await page.getByLabel("Value", { exact: true }).fill("anything");
    await runSearch(page);
    await expect(page.getByText("e2e-bad-search")).toBeVisible();
    await expect(count(page)).toHaveText("50");
    await expect(rows(page)).toHaveCount(50);
  });
});

test.describe("Messages panel — click-to-filter helpers", () => {
  test("the JSON coachmark is shown once and Got it persists the dismissal", async ({ page }) => {
    await page.goto(pagePath(SMALL_JSON));
    await expect(rows(page)).toHaveCount(2);
    await expect(page.getByText(COACHMARK_TEXT)).toBeVisible();
    await page.getByRole("button", { name: "Got it", exact: true }).click();
    await expect(page.getByText(COACHMARK_TEXT)).toHaveCount(0);
    expect(await page.evaluate((k) => localStorage.getItem(k), COACHMARK_KEY)).toBe("1");

    await page.reload();
    await expect(rows(page)).toHaveCount(2);
    await expect(page.getByText(COACHMARK_TEXT)).toHaveCount(0);
  });

  test("the coachmark stays hidden without an untruncated JSON row", async ({ page }) => {
    await page.goto(pagePath(SOURCE, "?limit=3"));
    await expect(count(page)).toHaveText("3");
    await expect(page.getByText(COACHMARK_TEXT)).toHaveCount(0);
  });

  test("clicking a JSON value fills a JSONPath search and Undo restores the previous input", async ({
    page,
  }) => {
    await page.goto(pagePath(SMALL_JSON));
    const small = rows(page).filter({ hasNotText: "preview" });
    await expect(small).toHaveCount(1);

    await openSearch(page);
    await page.getByLabel("Value", { exact: true }).fill("typed-before");

    await small.getByRole("button").first().click();
    await small.getByRole("button", { name: '"E2E-MASK-1"', exact: true }).click();

    await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("jsonpath");
    await expect(page.getByPlaceholder("Type or ↓ for top fields")).toHaveValue("$.order");
    await expect(page.getByLabel("Operator", { exact: true })).toHaveValue("eq");
    await expect(page.getByLabel("Value", { exact: true })).toHaveValue("E2E-MASK-1");
    await expect(page.getByText("Path replaced by click.")).toBeVisible();

    await page.getByRole("button", { name: "Undo", exact: true }).click();
    await expect(page.getByText("Path replaced by click.")).toHaveCount(0);
    // Undo restores path, operator and value; the mode stays JSONPath.
    await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("jsonpath");
    await expect(page.getByPlaceholder("Type or ↓ for top fields")).toHaveValue("");
    await expect(page.getByLabel("Operator", { exact: true })).toHaveValue("contains");
    await expect(page.getByLabel("Value", { exact: true })).toHaveValue("typed-before");
  });

  test("a click-to-filter without prior input opens search and shows no undo", async ({ page }) => {
    await page.goto(pagePath(SMALL_JSON));
    const small = rows(page).filter({ hasNotText: "preview" });
    await small.getByRole("button").first().click();
    await small.getByRole("button", { name: '"E2E-MASK-1"', exact: true }).click();

    await expect(page.getByRole("button", { name: "Close search", exact: true })).toBeVisible();
    await expect(page.getByPlaceholder("Type or ↓ for top fields")).toHaveValue("$.order");
    await expect(page.getByText("Path replaced by click.")).toHaveCount(0);
  });

  test("a row expands and collapses with the keyboard", async ({ page }) => {
    await page.goto(pagePath(SOURCE, "?limit=3"));
    const toggle = rows(page).first().getByRole("button").first();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await toggle.focus();
    await page.keyboard.press("Enter");
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(rows(page).first().getByText("(no key)")).toBeVisible();
    await page.keyboard.press("Space");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
  });
});
