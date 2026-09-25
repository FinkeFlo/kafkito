import { test, expect } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-walk-large";

test.describe("Messages list loading state", () => {
  test("shows a loading hint instead of claiming the topic is empty", async ({ page }) => {
    // Hold the first message list response open so the pre-resolve render
    // is observable. Without this the fetch resolves too quickly to assert
    // on. Match the list endpoint exactly: the page requests
    // /messages/count in the same tick, and holding that one instead lets
    // the list resolve before the assertion.
    let release: (() => void) | undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let heldOnce = false;

    const listPath = `/api/v1/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`;
    await page.route(
      (url) => url.pathname === listPath,
      async (route) => {
        if (!heldOnce) {
          heldOnce = true;
          await held;
        }
        await route.continue();
      },
    );

    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`,
    );

    // The bug: "No messages." was rendered while the very first page was
    // still in flight, telling the user the topic was empty when nothing
    // was known yet.
    await expect(page.getByText("Loading messages…")).toBeVisible();
    await expect(page.getByText("No messages.")).toHaveCount(0);
    expect(heldOnce).toBe(true);

    release?.();

    // Once data arrives the loading hint gives way to actual rows.
    await expect(page.getByTestId("message-row").first()).toBeVisible();
    await expect(page.getByText("Loading messages…")).toHaveCount(0);
  });

  test("still reports a genuinely empty topic as empty", async ({ page }) => {
    // Guards the other direction: the loading branch must not swallow the
    // real empty state.
    await page.route("**/api/v1/clusters/**/messages**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ messages: [] }),
      });
    });

    await page.goto(
      `/clusters/${encodeURIComponent(CLUSTER)}/topics/${encodeURIComponent(TOPIC)}/messages`,
    );

    await expect(page.getByText("No messages.")).toBeVisible();
    await expect(page.getByText("Loading messages…")).toHaveCount(0);
  });
});
