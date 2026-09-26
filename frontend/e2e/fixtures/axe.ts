import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, type TestInfo } from "@playwright/test";

export type Theme = "light" | "dark";
export const THEMES: readonly Theme[] = ["light", "dark"];

const THEME_STORAGE_KEY = "kafkito.theme";

// Only these impacts fail the walk. `minor` and `moderate` findings are
// still attached to the report so they stay visible.
const BLOCKING_IMPACTS = new Set(["serious", "critical"]);

/**
 * Pins the app theme before the first navigation, the same way the theme
 * walk does, and emulates the opposite system appearance so a scan can only
 * pass if the app really renders the pinned theme.
 */
export async function pinTheme(page: Page, theme: Theme): Promise<void> {
  await page.emulateMedia({ colorScheme: theme === "dark" ? "light" : "dark" });
  await page.addInitScript(([key, value]) => window.localStorage.setItem(key, value), [
    THEME_STORAGE_KEY,
    theme,
  ] as const);
}

export async function expectTheme(page: Page, theme: Theme): Promise<void> {
  if (theme === "dark") await expect(page.locator("html")).toHaveClass(/\bdark\b/);
  else await expect(page.locator("html")).not.toHaveClass(/\bdark\b/);
}

/**
 * Runs axe against the current page state and fails on any `serious` or
 * `critical` violation. The full result is attached to the test so the
 * HTML report (and the CI artifact) shows every finding, blocking or not.
 */
export async function expectNoBlockingA11yViolations(
  page: Page,
  testInfo: TestInfo,
  name: string,
): Promise<void> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"])
    .analyze();

  await testInfo.attach(`axe-${name}.json`, {
    body: JSON.stringify(results.violations, null, 2),
    contentType: "application/json",
  });

  const blocking = results.violations
    .filter((v) => BLOCKING_IMPACTS.has(v.impact ?? ""))
    .map((v) => ({
      rule: v.id,
      impact: v.impact,
      help: v.help,
      targets: v.nodes.map((n) => n.target.join(" ")),
    }));
  expect(blocking, `axe: serious/critical violations on ${name}`).toEqual([]);
}
