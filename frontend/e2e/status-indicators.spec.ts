import { test, expect, type Locator, type Page } from "@playwright/test";
import { hostAddress } from "./fixtures/host-address";
import {
  UNREACHABLE_CLUSTER as DOWN,
  withUnreachableCluster,
} from "./fixtures/unreachable-cluster";

// WCAG 1.4.1 "Use of Color": every status indicator must carry a cue that
// survives colour blindness — visible text, an icon with an accessible name,
// or a distinct shape. The walks click through the pages that show status
// and fail if an indicator is reduced to a bare coloured dot or tinted box.

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const GROUP = "e2e-idle-group";
const PRODUCE_TOPIC = "e2e-produce-target";

// Pixels that differ from the element's background, as a 0/1 string. Two
// indicators whose masks match differ only by colour.
async function inkMask(page: Page, target: Locator): Promise<string> {
  const box = await target.boundingBox();
  if (!box) throw new Error("indicator is not rendered");
  const pad = 3;
  const png = await page.screenshot({
    animations: "disabled",
    clip: {
      x: box.x - pad,
      y: box.y - pad,
      width: box.width + 2 * pad,
      height: box.height + 2 * pad,
    },
  });
  return page.evaluate(async (b64) => {
    const img = new Image();
    img.src = `data:image/png;base64,${b64}`;
    await img.decode();
    const canvas = document.createElement("canvas");
    canvas.width = img.width;
    canvas.height = img.height;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("no 2d context");
    ctx.drawImage(img, 0, 0);
    const d = ctx.getImageData(0, 0, img.width, img.height).data;
    let mask = "";
    for (let i = 0; i < d.length; i += 4) {
      const diff = Math.abs(d[i] - d[0]) + Math.abs(d[i + 1] - d[1]) + Math.abs(d[i + 2] - d[2]);
      mask += diff > 60 ? "1" : "0";
    }
    return mask;
  }, png.toString("base64"));
}

async function expectDifferentShapes(page: Page, a: Locator, b: Locator) {
  await page.mouse.move(0, 0);
  const [ma, mb] = [await inkMask(page, a), await inkMask(page, b)];
  expect(ma.length).toBe(mb.length);
  let differing = 0;
  for (let i = 0; i < ma.length; i++) if (ma[i] !== mb[i]) differing++;
  expect(differing / ma.length, "indicators differ only by colour").toBeGreaterThan(0.1);
}

test.describe("Status indicators never rely on colour alone", () => {
  test("cluster reachability in the fleet table, the KPI and the cluster picker", async ({
    page,
  }) => {
    await withUnreachableCluster(page);
    await page.goto("/clusters");

    const upRow = page.getByRole("row", { name: new RegExp(`\\b${CLUSTER}\\b`) });
    const downRow = page.getByRole("row", { name: new RegExp(DOWN) });
    const upDot = upRow.getByRole("img", { name: "healthy" });
    const downDot = downRow.getByRole("img", { name: "unhealthy" });
    await expect(upDot).toBeVisible();
    await expect(downDot).toBeVisible();
    await expect(downRow.getByText("UNREACHABLE", { exact: true })).toBeVisible();
    await expectDifferentShapes(page, upDot, downDot);

    const kpi = page.getByText("Unreachable now").locator("..");
    await expect(kpi.getByRole("img", { name: "Needs attention" })).toBeVisible();
    await expect(kpi).toContainText(DOWN);

    const pill = page.getByRole("button", { name: /^Cluster: .+, (reachable|unreachable)$/ });
    await expect(pill).toBeVisible();
    await pill.click();
    const listbox = page.getByRole("listbox", { name: /select cluster/i });
    const upOption = listbox.getByRole("option", { name: new RegExp(`\\b${CLUSTER}\\b`) });
    const downOption = listbox.getByRole("option", { name: new RegExp(DOWN) });
    await expect(upOption.getByRole("img", { name: "reachable" })).toBeVisible();
    await expect(downOption.getByRole("img", { name: "unreachable" })).toBeVisible();
    await expectDifferentShapes(
      page,
      upOption.getByRole("img", { name: "reachable" }),
      downOption.getByRole("img", { name: "unreachable" }),
    );
  });

  test("the fleet KPI shows a labelled icon when all clusters are reachable", async ({ page }) => {
    await page.goto("/clusters");
    const kpi = page.getByText("Unreachable now").locator("..");
    await expect(kpi.getByRole("img", { name: "Good" })).toBeVisible();
    await expect(kpi).toContainText("none");
    await expect(kpi).not.toContainText(/[+−±]none/);
  });

  test("brokers, consumer group state, lag and the reset-offsets selection", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);

    await page
      .getByRole("navigation", { name: "Main" })
      .getByRole("link", { name: "Brokers" })
      .click();
    const brokerRow = page
      .getByRole("row")
      .filter({ has: page.getByRole("img", { name: "healthy" }) });
    await expect(brokerRow.first()).toBeVisible();

    await page
      .getByRole("navigation", { name: "Main" })
      .getByRole("link", { name: "Consumer groups" })
      .click();
    // Group rows are keyboard-activatable (role="button").
    const groupRow = page.getByRole("button", { name: new RegExp(GROUP) });
    await expect(groupRow.getByText("Empty", { exact: true })).toBeVisible();
    // The lag bucket is named for screen readers and has a visible glyph.
    await expect(groupRow.getByText(/^(normal|elevated|critical) lag/)).toBeAttached();
    await expect(groupRow.getByText(/^(·|▲|▲▲)$/)).toBeVisible();

    await groupRow.click();
    await expect(page).toHaveURL(new RegExp(`group=${GROUP}`));
    await expect(page.getByText("Empty", { exact: true }).last()).toBeVisible();

    await page.getByRole("button", { name: /^reset offsets/i }).click();
    const modal = page.getByRole("dialog", { name: /reset offsets/i });
    await expect(modal).toBeVisible();
    const selected = modal.getByRole("img", { name: "selected" });
    const previewRows = modal.getByRole("row").filter({ hasText: /^\s*p\d+/ });
    await expect(previewRows.first()).toBeVisible();
    const total = await previewRows.count();
    await expect(selected).toHaveCount(total);

    // The checkbox is visually hidden; users click its chip label.
    await modal.locator("label", { has: page.getByRole("checkbox", { name: "p0" }) }).click();
    await expect(modal.getByRole("checkbox", { name: "p0" })).not.toBeChecked();
    await expect(selected).toHaveCount(total - 1);
    await expect(
      previewRows.filter({ hasText: /^\s*p0\b/ }).getByRole("img", { name: "selected" }),
    ).toHaveCount(0);
    await page.keyboard.press("Escape");
  });

  test("the produce result carries a labelled success icon", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);
    await page.getByRole("row", { name: new RegExp(`^${PRODUCE_TOPIC}\\b`) }).click();
    await page
      .getByRole("navigation", { name: "Topic sections" })
      .getByRole("link", { name: /produce/i })
      .click();
    await page.getByPlaceholder('{"hello":"world"}').fill('{"status":"indicator"}');
    await page.getByRole("button", { name: "Produce", exact: true }).click();

    const result = page.getByRole("status").filter({ hasText: /Produced · partition/ });
    await expect(result).toBeVisible();
    await expect(result.getByRole("img", { name: "Success" })).toBeVisible();
  });

  test("connection test results and toasts pair colour with a labelled icon", async ({ page }) => {
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/topics`);
    await page.getByRole("button", { name: /^Cluster: / }).click();
    await page.getByRole("link", { name: /manage clusters/i }).click();
    await page.getByRole("button", { name: "Add cluster" }).click();
    const dialog = page.getByRole("dialog", { name: "Add private cluster" });
    await expect(dialog).toBeVisible();

    await dialog.getByRole("textbox", { name: /^Name/ }).fill(`e2e-status-${Date.now()}`);
    const brokers = dialog.getByRole("textbox", { name: /^Brokers/ });
    const testButton = dialog.getByRole("button", { name: "Test connection" });

    // The backend refuses loopback brokers for private clusters, which gives
    // a real failure here.
    await brokers.fill("localhost:1");
    await testButton.click();
    const failed = dialog.getByRole("alert").filter({ hasText: /blocked address/ });
    await expect(failed).toBeVisible({ timeout: 20_000 });
    const errorIcon = failed.getByRole("img", { name: "Error" });
    await expect(errorIcon).toBeVisible();
    const errorMask = await inkMask(page, errorIcon);

    // The fixture broker through a private host address is a real success.
    await brokers.fill(`${hostAddress()}:39092`);
    await testButton.click();
    const ok = dialog.getByRole("status").filter({ hasText: /^OK — reachable/ });
    await expect(ok).toBeVisible({ timeout: 20_000 });
    const okIcon = ok.getByRole("img", { name: "Success" });
    await expect(okIcon).toBeVisible();
    const okMask = await inkMask(page, okIcon);
    let differing = 0;
    for (let i = 0; i < okMask.length; i++) if (okMask[i] !== errorMask[i]) differing++;
    expect(
      differing / okMask.length,
      "success and error icons differ only by colour",
    ).toBeGreaterThan(0.05);

    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    const toast = page.locator('[data-sonner-toast][data-type="success"]');
    await expect(toast).toContainText("Saved");
    await expect(toast.locator("[data-icon] svg")).toBeVisible();
  });
});
