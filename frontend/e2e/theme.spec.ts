import { test, expect, type Page } from "@playwright/test";

const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const STORAGE_KEY = "kafkito.theme";

/**
 * `color-scheme` decides how the OS paints the native widgets we do not render
 * ourselves: `<select>` popups, scrollbars, date pickers. If the app leaves it
 * unset, those widgets follow the *system* appearance instead of the app
 * theme — e.g. a dark select popup on kafkito's light canvas.
 *
 * The default theme preference is "system", so app and OS can only disagree
 * once the user explicitly pins a theme. Each test pins one and emulates the
 * opposite system appearance, which is the only configuration in which this
 * can regress.
 */
test.describe("Native control appearance follows the app theme", () => {
  async function pinTheme(page: Page, theme: "light" | "dark") {
    await page.addInitScript(
      ([key, value]) => window.localStorage.setItem(key, value),
      [STORAGE_KEY, theme] as const,
    );
  }

  const colorScheme = (page: Page) =>
    page.evaluate(() => getComputedStyle(document.documentElement).colorScheme);

  test("pinned light stays light on a dark-mode system", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await pinTheme(page, "light");
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);

    // Guard the premise: without this the assertion below could pass simply
    // because the app happened to be light for an unrelated reason.
    await expect(page.locator("html")).not.toHaveClass(/\bdark\b/);
    await expect.poll(() => colorScheme(page)).toBe("light");
  });

  test("pinned dark stays dark on a light-mode system", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "light" });
    await pinTheme(page, "dark");
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);

    await expect(page.locator("html")).toHaveClass(/\bdark\b/);
    await expect.poll(() => colorScheme(page)).toBe("dark");
  });
});
