import { test, expect, type Locator, type Page } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-walk-target";
const GROUP = "e2e-idle-group";
const c = encodeURIComponent(CLUSTER);

const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// Presses Tab until `target` has focus. A table row must never take focus on
// the way: the row's action lives in the control inside its primary cell.
async function tabTo(page: Page, target: Locator, maxPresses = 60): Promise<void> {
  for (let i = 0; i < maxPresses; i++) {
    await page.keyboard.press("Tab");
    const tag = await page.evaluate(() => document.activeElement?.tagName);
    expect(tag, "a table row must not be a tab stop").not.toBe("TR");
    if (await target.evaluate((el) => el === document.activeElement)) return;
  }
  throw new Error(`Tab did not reach the target within ${maxPresses} presses`);
}

async function expectNoRowRoleOverride(page: Page): Promise<void> {
  await expect(page.locator("tr[role], tr[tabindex]")).toHaveCount(0);
}

// The field is marked invalid and its description is the visible error text,
// which carries a named icon so the state does not rely on colour. The error
// sits in a polite live region (never role=alert), so typing into a live
// validated field is not interrupted.
async function expectFieldError(page: Page, control: Locator, message: string): Promise<void> {
  await expect(control).toHaveAttribute("aria-invalid", "true");
  const describedBy = await control.getAttribute("aria-describedby");
  expect(describedBy, "aria-describedby must reference the error").toBeTruthy();
  const error = page.locator(`[id="${describedBy}"]`);
  await expect(error).toBeVisible();
  await expect(error).toContainText(message);
  await expect(error.getByRole("img", { name: "Error" })).toBeVisible();
  await expect(error).not.toHaveAttribute("role");
  await expect(page.locator(`[aria-live="polite"]:has(> [id="${describedBy}"])`)).toHaveCount(1);
  await expect(control).toHaveAccessibleDescription(new RegExp(escapeRegExp(message)));
}

async function expectFieldValid(control: Locator): Promise<void> {
  await expect(control).not.toHaveAttribute("aria-invalid");
  await expect(control).not.toHaveAttribute("aria-describedby");
}

test.describe("clickable table rows keep table semantics", () => {
  test("topics: Tab reaches the topic link in the name cell and Enter opens it", async ({
    page,
  }) => {
    await page.goto(`/clusters/${c}/topics`);
    const row = page.getByRole("row", { name: new RegExp(TOPIC) });
    await expect(row).toBeVisible();
    await expectNoRowRoleOverride(page);

    // Keyboard only: "/" focuses the filter, then Tab walks to the row link.
    await page.keyboard.press("/");
    await page.keyboard.type(TOPIC);
    const link = row.getByRole("link", { name: TOPIC, exact: true });
    await tabTo(page, link);
    await expect(link).toBeFocused();
    await page.keyboard.press("Enter");

    await expect(page).toHaveURL(new RegExp(`/clusters/${c}/topics/${TOPIC}(/|$)`));
    await expect(page.getByRole("heading", { level: 1, name: TOPIC, exact: true })).toBeVisible();
  });

  test("topics: a click elsewhere on the row still opens the topic", async ({ page }) => {
    await page.goto(`/clusters/${c}/topics`);
    const row = page.getByRole("row", { name: new RegExp(TOPIC) });
    // The partitions cell holds no control of its own.
    await row.getByRole("cell").nth(1).click();
    await expect(page).toHaveURL(new RegExp(`/clusters/${c}/topics/${TOPIC}(/|$)`));
  });

  test("groups: the group button toggles the detail with Enter and Space", async ({ page }) => {
    await page.goto(`/clusters/${c}/groups`);
    const row = page.getByRole("row", { name: new RegExp(GROUP) });
    await expect(row).toBeVisible();
    await expectNoRowRoleOverride(page);

    await page.keyboard.press("/");
    await page.keyboard.type(GROUP);
    const toggle = row.getByRole("button", { name: GROUP, exact: true });
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await tabTo(page, toggle);

    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(new RegExp(`group=${GROUP}`));
    await expect(page.getByText("Offsets", { exact: true })).toBeVisible();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(toggle).toBeFocused();

    await page.keyboard.press("Space");
    await expect(page).not.toHaveURL(/group=/);
    await expect(page.getByText("Offsets", { exact: true })).toBeHidden();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
  });

  test("groups: a click on the state cell still opens the detail", async ({ page }) => {
    await page.goto(`/clusters/${c}/groups`);
    const row = page.getByRole("row", { name: new RegExp(GROUP) });
    await row.getByText("Empty", { exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`group=${GROUP}`));
    await expect(row.getByRole("button", { name: GROUP, exact: true })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });
});

test.describe("form errors are announced, not only coloured", () => {
  test("produce: invalid JSON marks the value field and describes it", async ({ page }) => {
    await page.goto(`/clusters/${c}/topics/${encodeURIComponent(TOPIC)}/produce`);
    const value = page.getByRole("textbox", { name: "Value", exact: true });
    await expectFieldValid(value);

    await value.fill("{not json");
    await page.getByRole("button", { name: "Format JSON" }).click();
    await expectFieldError(page, value, "Value is not valid JSON");

    // Editing the value clears the error.
    await value.fill('{"ok":true}');
    await expectFieldValid(value);
    await expect(page.getByText(/^Value is not valid JSON/)).toBeHidden();
  });

  test("SCRAM user: out-of-range iterations mark the field and block Create", async ({ page }) => {
    await page.goto(`/clusters/${c}/security/users`);
    await page.getByRole("button", { name: "+ New User" }).click();
    const dialog = page.getByRole("dialog", { name: "Create / Update SCRAM User" });
    await dialog.getByPlaceholder("alice").fill("e2e-invalid-iterations");
    await dialog.getByPlaceholder("at least 1 character").fill("pw");
    const iterations = dialog.getByRole("spinbutton");
    const create = dialog.getByRole("button", { name: "Create", exact: true });
    await expectFieldValid(iterations);
    await expect(create).toBeEnabled();

    await iterations.fill("100");
    await expectFieldError(page, iterations, "Iterations must be between 4096 and 16384.");
    await expect(create).toBeDisabled();

    await iterations.fill("8192");
    await expectFieldValid(iterations);
    await expect(create).toBeEnabled();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toBeHidden();
  });

  test("reset offsets: a non-numeric offset marks the field", async ({ page }) => {
    await page.goto(`/clusters/${c}/groups?group=${encodeURIComponent(GROUP)}`);
    await page.getByRole("button", { name: /^reset offsets/i }).click();
    const dialog = page.getByRole("dialog", { name: /reset offsets/i });
    await dialog.getByRole("combobox", { name: "Strategy" }).selectOption("offset");
    const offset = dialog.getByRole("textbox", { name: /^Offset/ });
    await expectFieldValid(offset);

    await offset.fill("abc");
    await expectFieldError(page, offset, "Enter a numeric offset.");
    await expect(dialog.getByRole("button", { name: /commit reset/i })).toBeDisabled();

    await offset.fill("0");
    await expectFieldValid(offset);
    await page.keyboard.press("Escape");
  });
});
