import { test as base, type Page } from "@playwright/test";

const STORAGE_KEY = "kafkito.private-clusters.v1";

export const SECOND_CLUSTER_NAME = "e2e-second";
export const SR_CLUSTER_NAME = "e2e-sr";

type Fixtures = {
  page: Page;
  pageWithSRCluster: Page;
};

export const test = base.extend<Fixtures>({
  page: async ({ page }, use) => {
    await page.addInitScript(
      ([key, name]) => {
        const cluster = {
          id: `pc_${name}`,
          name,
          brokers: ["localhost:1"],
          auth: { type: "none" },
          tls: { enabled: false },
          created_at: 0,
          updated_at: 0,
        };
        window.localStorage.setItem(key, JSON.stringify([cluster]));
      },
      [STORAGE_KEY, SECOND_CLUSTER_NAME],
    );
    await use(page);
    // Defensive: if the test failed before page.goto reached the app origin
    // (e.g., ERR_CONNECTION_REFUSED on first navigation), the page is on
    // chrome-error:// or about:blank where Chromium denies localStorage
    // access. Swallow the SecurityError so the underlying test failure is
    // what surfaces, not noise from teardown. The browser context is torn
    // down anyway, so leaking the seeded entry has no cross-test impact.
    await page
      .evaluate((key) => {
        try {
          window.localStorage.removeItem(key);
        } catch {
          // page not on app origin — nothing to clean
        }
      }, STORAGE_KEY)
      .catch(() => {
        // page.evaluate itself failed (e.g., page closed) — nothing to clean
      });
  },
  pageWithSRCluster: async ({ browser }, use) => {
    const context = await browser.newContext();
    await context.addInitScript(
      ([key, name]) => {
        const cluster = {
          id: `pc_${name}`,
          name,
          brokers: ["localhost:1"],
          auth: { type: "none" },
          tls: { enabled: false },
          schema_registry: { url: "http://localhost:1" },
          created_at: 0,
          updated_at: 0,
        };
        window.localStorage.setItem(key, JSON.stringify([cluster]));
      },
      [STORAGE_KEY, SR_CLUSTER_NAME],
    );
    const page = await context.newPage();
    await use(page);
    await context.close();
  },
});

export { expect } from "@playwright/test";
