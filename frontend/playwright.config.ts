import { defineConfig, devices } from "@playwright/test";

const BASE_URL = process.env.KAFKITO_E2E_BASE_URL ?? "http://localhost:47421";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.spec.ts",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  // In CI the list reporter adds per-test durations to the job log, so a
  // slowdown of the suite can be traced to the tests that caused it.
  reporter: process.env.CI ? [["github"], ["list"]] : [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
