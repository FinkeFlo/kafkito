import { test, expect, type Page } from "@playwright/test";

/**
 * Walks the private-cluster and theme flows against the Go-served build, where
 * the Content-Security-Policy is active, and fails on any CSP violation.
 *
 * Private-cluster credentials live in localStorage, so the CSP is what keeps an
 * XSS from turning into a credential leak; this walk proves the app works
 * without loosening it (no inline scripts, no injected <style> elements).
 *
 * Uses the plain Playwright `test` (not the private-cluster fixture): the
 * fixture re-seeds localStorage on every navigation, which would hide whether
 * the cluster created through the UI survives a reload.
 */

const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const CLUSTER = "e2e-csp";
// Loopback is rejected by the server's SSRF pre-check, so the topics request
// fails fast; the walk only needs to observe that the header is sent.
const BROKER = "localhost:1";
const PASSWORD = "e2e-csp-secret-pw";

async function collectCSPViolations(page: Page): Promise<string[]> {
  const violations: string[] = [];
  page.on("console", (msg) => {
    if (/Content[ -]Security[ -]Policy/i.test(msg.text())) violations.push(msg.text());
  });
  // exposeBinding survives navigations, so violations from every page load of
  // the walk end up in the same list.
  await page.exposeBinding("__reportCSPViolation", (_source, v: string) => {
    violations.push(v);
  });
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => {
      const report = (window as unknown as { __reportCSPViolation: (v: string) => void })
        .__reportCSPViolation;
      report(`${e.violatedDirective} blocked=${e.blockedURI} sample=${e.sample}`);
    });
  });
  return violations;
}

test.describe("Content-Security-Policy", () => {
  test("private cluster and theme flows run without CSP violations", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "light" });
    const violations = await collectCSPViolations(page);

    // Guard the premise: the build under test is served by the Go binary with
    // the policy, not by a dev server without it.
    const res = await page.goto("/settings/clusters");
    const csp = res?.headers()["content-security-policy"] ?? "";
    expect(csp).toContain("script-src 'self'");
    expect(csp).toContain("style-src 'self'");
    expect(csp).not.toContain("unsafe-inline");

    // Create a private cluster through the form.
    await page.getByRole("button", { name: "Add cluster" }).click();
    const dialog = page.getByRole("dialog", { name: "Add private cluster" });
    await dialog.getByPlaceholder("my-dev-cluster").fill(CLUSTER);
    await dialog.getByPlaceholder("host1:9092, host2:9092").fill(BROKER);
    await dialog.getByLabel("Auth type").selectOption("plain");
    await dialog.getByLabel(/^Username/).fill("e2e-user");
    await dialog.getByLabel(/^Password/).fill(PASSWORD);
    await dialog.getByRole("button", { name: "Save" }).click();

    // sonner's stylesheet ships as a file, not an injected <style>; if it
    // were blocked the toaster would lose `position: fixed`.
    await expect(page.getByText(`Saved "${CLUSTER}"`)).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(() => {
          const el = document.querySelector("[data-sonner-toaster]");
          return el ? getComputedStyle(el).position : null;
        }),
      )
      .toBe("fixed");
    const row = page.getByRole("row").filter({ hasText: CLUSTER });
    await expect(row).toBeVisible();

    // It survives a reload (stored in localStorage).
    await page.reload();
    await expect(page.getByRole("row").filter({ hasText: CLUSTER })).toBeVisible();

    // Open its topics via the cluster picker; the request must carry the
    // private-cluster header.
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);
    const topicsRequest = page.waitForRequest(
      (req) =>
        req.url().includes("/api/v1/clusters/__private__/topics") &&
        !!req.headers()["x-kafkito-cluster"],
    );
    await page.getByRole("button", { name: new RegExp(`^Cluster: ${PRIMARY}`) }).click();
    await page
      .getByRole("listbox", { name: /select cluster/i })
      .getByRole("option", { name: new RegExp(CLUSTER) })
      .click();
    await expect(page).toHaveURL(new RegExp(`/clusters/${CLUSTER}/topics$`));
    const header = (await topicsRequest).headers()["x-kafkito-cluster"];
    const sent = JSON.parse(atob(header)) as { name: string; brokers: string[] };
    expect(sent.name).toBe(CLUSTER);
    expect(sent.brokers).toEqual([BROKER]);
    // The server's error for this cluster must not echo the credentials.
    await expect(page.locator("body")).not.toContainText(PASSWORD);

    // Theme toggle still works under the CSP...
    await expect(page.locator("html")).not.toHaveClass(/\bdark\b/);
    await page.getByRole("button", { name: "Switch to dark theme" }).click();
    await expect(page.locator("html")).toHaveClass(/\bdark\b/);

    // ...and the persisted dark theme is applied before the first frame is
    // rendered (no flash of the light theme): /theme-init.js must have run by
    // the first requestAnimationFrame, long before the React bundle applies
    // the theme itself.
    await page.addInitScript(() => {
      requestAnimationFrame(() => {
        (window as unknown as { __darkAtFirstFrame: boolean }).__darkAtFirstFrame =
          document.documentElement.classList.contains("dark");
      });
    });
    await page.reload();
    await expect
      .poll(() =>
        page.evaluate(
          () => (window as unknown as { __darkAtFirstFrame?: boolean }).__darkAtFirstFrame,
        ),
      )
      .toBe(true);
    await expect(page.locator("html")).toHaveClass(/\bdark\b/);

    expect(violations, violations.join("\n")).toEqual([]);
  });
});
