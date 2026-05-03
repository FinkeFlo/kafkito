import { test as base, type Page } from "@playwright/test";

const STORAGE_KEY = "kafkito.private-clusters.v1";

export const SECOND_CLUSTER_NAME = "e2e-second";

type SeededPage = { page: Page };

export const test = base.extend<SeededPage>({
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
    await page.evaluate((key) => window.localStorage.removeItem(key), STORAGE_KEY);
  },
});

export { expect } from "@playwright/test";
