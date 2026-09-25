import { networkInterfaces } from "node:os";
import { test, expect, type Page } from "@playwright/test";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const KAFKA_HOST_PORT = 39092;

// The backend refuses loopback brokers for private clusters (SSRF guard), so
// the connection test dials the fixture broker through a private host
// address. The docker compose broker is published on all host interfaces.
function hostAddress(): string {
  const override = process.env.KAFKITO_E2E_HOST_IP;
  if (override) return override;
  const privateV4 = /^(10\.|172\.(1[6-9]|2\d|3[01])\.|192\.168\.)/;
  for (const addrs of Object.values(networkInterfaces())) {
    for (const a of addrs ?? []) {
      if (a.family === "IPv4" && !a.internal && privateV4.test(a.address)) return a.address;
    }
  }
  throw new Error("no private IPv4 host address found; set KAFKITO_E2E_HOST_IP");
}

async function openAddClusterDialog(page: Page) {
  await page.goto("/settings/clusters");
  await page.getByRole("button", { name: "Add cluster" }).click();
  const dialog = page.getByRole("dialog", { name: "Add private cluster" });
  await expect(dialog).toBeVisible();
  return dialog;
}

async function testConnection(page: Page, brokers: string) {
  const dialog = await openAddClusterDialog(page);
  await dialog.getByRole("textbox", { name: /^Name\*?$/ }).fill("e2e-test-connection");
  await dialog.getByRole("textbox", { name: /^Brokers \(comma-separated\)/ }).fill(brokers);
  const response = page.waitForResponse(
    (r) => r.url().endsWith("/api/v1/clusters/_test") && r.request().method() === "POST",
  );
  await dialog.getByRole("button", { name: "Test connection" }).click();
  return { dialog, response: await response };
}

test.describe("Clusters", () => {
  test("cluster list loads the configured cluster as reachable", async ({ page }) => {
    const clusters = page.waitForResponse((r) => r.url().endsWith("/api/v1/clusters"));
    await page.goto("/clusters");
    expect((await clusters).status()).toBe(200);

    const row = page.getByRole("row", { name: new RegExp(CLUSTER) });
    await expect(row).toBeVisible();
    await expect(row).not.toContainText("UNREACHABLE");
  });

  test("brokers view renders the fixture broker", async ({ page }) => {
    const brokers = page.waitForResponse((r) =>
      r.url().endsWith(`/api/v1/clusters/${encodeURIComponent(CLUSTER)}/brokers`),
    );
    await page.goto(`/clusters/${encodeURIComponent(CLUSTER)}/brokers`);
    expect((await brokers).status()).toBe(200);

    await expect(page.getByRole("heading", { name: "Brokers", level: 1 })).toBeVisible();
    const row = page.getByRole("row", { name: new RegExp(String(KAFKA_HOST_PORT)) });
    await expect(row).toBeVisible();
    await expect(row).toContainText("controller");
  });

  test("capabilities endpoints return the probe and refresh it", async ({ request }) => {
    const base = `/api/v1/clusters/${encodeURIComponent(CLUSTER)}`;

    const caps = await request.get(`${base}/capabilities`);
    expect(caps.status()).toBe(200);
    const body = await caps.json();
    expect(body.cluster).toBe(CLUSTER);
    expect(body.capabilities.list_topics).toBe(true);

    const refreshed = await request.post(`${base}/capabilities/refresh`);
    expect(refreshed.status()).toBe(200);
    expect((await refreshed.json()).capabilities.describe_cluster).toBe(true);

    const unknown = await request.get("/api/v1/clusters/e2e-no-such-cluster/capabilities");
    expect(unknown.status()).toBe(404);
    expect(await unknown.json()).toEqual({ error: "unknown cluster: e2e-no-such-cluster" });
  });

  test("test connection reports a reachable cluster", async ({ page }) => {
    const { dialog, response } = await testConnection(page, `${hostAddress()}:${KAFKA_HOST_PORT}`);
    expect(response.status()).toBe(200);
    await expect(dialog.getByText("OK — reachable (none, TLS: no)")).toBeVisible();
  });

  test("test connection reports an unreachable cluster", async ({ page }) => {
    // The server-side probe gives up after its connection timeout (15s) when
    // the port is filtered instead of refused.
    test.setTimeout(60_000);
    const { dialog, response } = await testConnection(page, `${hostAddress()}:1`);
    expect(response.status()).toBe(200);
    await expect(dialog.getByText(/^Unreachable: /)).toBeVisible();
  });

  test("test connection shows the validation error for an invalid config", async ({ page }) => {
    const { dialog, response } = await testConnection(page, ",");
    expect(response.status()).toBe(400);
    expect(await response.json()).toEqual({
      error: 'request body "/brokers": must have at least 1 items',
      code: "invalid_request",
    });
    await expect(
      dialog.getByText('Error: HTTP 400: request body "/brokers": must have at least 1 items'),
    ).toBeVisible();
  });

  test("test connection rejects a loopback broker", async ({ page }) => {
    const { dialog, response } = await testConnection(page, `localhost:${KAFKA_HOST_PORT}`);
    expect(response.status()).toBe(400);
    await expect(dialog.getByText(/^Error: HTTP 400: broker "localhost:39092": /)).toBeVisible();
  });
});
