import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { test, expect, type Page } from "@playwright/test";
import { pinTheme, THEMES } from "./fixtures/axe";
import { withUnreachableCluster } from "./fixtures/unreachable-cluster";

// Evidence screenshots of the status pages as people with red-green colour
// blindness see them, via Chromium's vision-deficiency emulation. Opt-in:
// set KAFKITO_E2E_VISION_DIR to the output folder.
const OUT = process.env.KAFKITO_E2E_VISION_DIR;
const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const GROUP = "e2e-idle-group";
const VISIONS = ["none", "deuteranopia", "protanopia"] as const;

type Shot = { name: string; open: (page: Page) => Promise<void> };

const c = encodeURIComponent(CLUSTER);

const SHOTS: Shot[] = [
  {
    name: "fleet",
    open: async (page) => {
      await page.goto("/clusters");
      await expect(page.getByRole("img", { name: "unhealthy" })).toBeVisible();
    },
  },
  {
    name: "cluster-picker",
    open: async (page) => {
      await page.goto(`/clusters/${c}/topics`);
      await page.getByRole("button", { name: /^Cluster: / }).click();
      await expect(page.getByRole("img", { name: "unreachable" })).toBeVisible();
    },
  },
  {
    name: "brokers",
    open: async (page) => {
      await page.goto(`/clusters/${c}/brokers`);
      await expect(page.getByRole("img", { name: "healthy" }).first()).toBeVisible();
    },
  },
  {
    name: "groups",
    open: async (page) => {
      await page.goto(`/clusters/${c}/groups`);
      await expect(page.getByRole("button", { name: new RegExp(GROUP) })).toBeVisible();
    },
  },
  {
    name: "reset-offsets-preview",
    open: async (page) => {
      await page.goto(`/clusters/${c}/groups?group=${GROUP}`);
      await page.getByRole("button", { name: /^reset offsets/i }).click();
      const modal = page.getByRole("dialog", { name: /reset offsets/i });
      await modal.locator("label", { has: page.getByRole("checkbox", { name: "p0" }) }).click();
      await expect(modal.getByRole("img", { name: "selected" }).first()).toBeVisible();
    },
  },
  {
    name: "produce-result",
    open: async (page) => {
      await page.goto(`/clusters/${c}/topics/e2e-produce-target/produce`);
      await page.getByPlaceholder('{"hello":"world"}').fill('{"vision":"check"}');
      await page.getByRole("button", { name: "Produce", exact: true }).click();
      await expect(page.getByRole("img", { name: "Success" })).toBeVisible();
    },
  },
  {
    name: "connection-test-error",
    open: async (page) => {
      await page.goto(`/settings/clusters?cluster=${c}`);
      await page.getByRole("button", { name: "Add cluster" }).click();
      const dialog = page.getByRole("dialog", { name: "Add private cluster" });
      await dialog.getByRole("textbox", { name: /^Name/ }).fill("vision-check");
      await dialog.getByRole("textbox", { name: /^Brokers/ }).fill("localhost:1");
      await dialog.getByRole("button", { name: "Test connection" }).click();
      await expect(dialog.getByRole("img", { name: "Error" })).toBeVisible({ timeout: 20_000 });
    },
  },
];

test.describe("Colour-vision evidence screenshots", () => {
  test.skip(!OUT, "set KAFKITO_E2E_VISION_DIR to capture the screenshots");
  test.skip(({ browserName }) => browserName !== "chromium", "needs the Chromium CDP");

  for (const theme of THEMES) {
    for (const shot of SHOTS) {
      test(`${shot.name} (${theme})`, async ({ page }) => {
        const dir = OUT as string;
        mkdirSync(dir, { recursive: true });
        await pinTheme(page, theme);
        await withUnreachableCluster(page);
        await shot.open(page);
        await page.mouse.move(0, 0);
        const cdp = await page.context().newCDPSession(page);
        for (const type of VISIONS) {
          await cdp.send("Emulation.setEmulatedVisionDeficiency", { type });
          await page.screenshot({
            path: join(dir, `${shot.name}-${theme}-${type}.png`),
            animations: "disabled",
          });
        }
      });
    }
  }
});
