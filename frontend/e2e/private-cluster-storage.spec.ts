import { test, expect, type Page } from "@playwright/test";
import { privateClusterBroker } from "./fixtures/host-address";

/**
 * Saved private clusters (with their passwords) live in localStorage under
 * kafkito.private-clusters.v1. This walk seeds that key with the literal shape
 * today's settings form writes, then clicks through the app: the connections
 * must show up, work, and be sent to the server only in the X-Kafkito-Cluster
 * header, never echoed to the console or the page.
 *
 * Uses the plain Playwright `test`: the private-cluster fixture overwrites the
 * key with its own entry.
 */

const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const STORAGE_KEY = "kafkito.private-clusters.v1";
const WORKING = "e2e-v1";
const SCRAM = "e2e-v1-scram";
const SCRAM_PASSWORD = "e2e-v1-scram-secret-pw";
const SR_PASSWORD = "e2e-v1-sr-secret-pw";
const FIXTURE_TOPIC = "e2e-walk-target";

function v1Payload(broker: string): string {
  return JSON.stringify([
    {
      id: "5b1c7e0a-3f2d-4c8b-9a6e-1d2f3a4b5c6d",
      name: WORKING,
      is_prod: true,
      brokers: [broker],
      auth: { type: "none" },
      tls: { enabled: false, insecure_skip_verify: false },
      schema_registry: {
        url: "http://10.255.255.1:8081",
        username: "sr-user",
        password: SR_PASSWORD,
        insecure_skip_verify: true,
      },
      created_at: 1767261600000,
      updated_at: 1767348000000,
    },
    {
      id: "pc_0f1e2d3c4b5a6978",
      name: SCRAM,
      is_prod: false,
      brokers: ["localhost:1"],
      auth: { type: "scram-sha-512", username: "svc-e2e", password: SCRAM_PASSWORD },
      tls: { enabled: true, insecure_skip_verify: true },
      created_at: 1767261600000,
      updated_at: 1767261600000,
    },
  ]);
}

// Seeds only an empty key, so the final assertion sees what the app left
// behind rather than a fresh copy from the init script.
async function seed(page: Page, payload: string) {
  await page.addInitScript(
    ([key, value]) => {
      if (window.localStorage.getItem(key) === null) window.localStorage.setItem(key, value);
    },
    [STORAGE_KEY, payload],
  );
}

function collectConsole(page: Page): string[] {
  const lines: string[] = [];
  page.on("console", (msg) => lines.push(msg.text()));
  page.on("pageerror", (err) => lines.push(`${err.message}\n${err.stack ?? ""}`));
  return lines;
}

async function pickCluster(page: Page, name: string) {
  await page.getByRole("button", { name: /^Cluster: / }).click();
  await page
    .getByRole("listbox", { name: /select cluster/i })
    .getByRole("option", { name: new RegExp(`(^|\\s)${name}\\s`) })
    .click();
  await expect(page).toHaveURL(new RegExp(`/clusters/${name}/topics$`));
}

test.describe("Private-cluster storage v1", () => {
  test("stored connections load, reach the broker and keep their secrets out of logs", async ({
    page,
  }) => {
    const broker = privateClusterBroker();
    const payload = v1Payload(broker);
    await seed(page, payload);
    const consoleLines = collectConsole(page);
    const headers: string[] = [];
    page.on("request", (req) => {
      const h = req.headers()["x-kafkito-cluster"];
      if (h) headers.push(h);
    });

    // Both stored connections are listed.
    await page.goto("/settings/clusters");
    await expect(page.getByRole("row").filter({ hasText: WORKING }).first()).toBeVisible();
    await expect(page.getByRole("row").filter({ hasText: SCRAM })).toBeVisible();

    // Clicking the working one loads its topics through the private-cluster
    // path, with the stored config in the header.
    await page.goto(`/clusters/${encodeURIComponent(PRIMARY)}/topics`);
    const topicsRequest = page.waitForRequest(
      (req) =>
        req.method() === "GET" &&
        new URL(req.url()).pathname === "/api/v1/clusters/__private__/topics" &&
        !!req.headers()["x-kafkito-cluster"],
    );
    await pickCluster(page, WORKING);
    const request = await topicsRequest;
    await expect(page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC) })).toBeVisible();
    await expect(
      page.getByRole("button", { name: new RegExp(`^Cluster: ${WORKING}`) }),
    ).toBeVisible();

    const header = request.headers()["x-kafkito-cluster"];
    expect(JSON.parse(atob(header))).toEqual({
      name: WORKING,
      is_prod: true,
      brokers: [broker],
      auth: { type: "none" },
      tls: { enabled: false, insecure_skip_verify: false },
      schema_registry: {
        url: "http://10.255.255.1:8081",
        username: "sr-user",
        password: SR_PASSWORD,
        insecure_skip_verify: true,
      },
    });

    // The SCRAM connection fails (loopback is refused server-side); its
    // credentials go out in the header but never show up in the error.
    const scramRequest = page.waitForRequest(
      (req) =>
        new URL(req.url()).pathname === "/api/v1/clusters/__private__/topics" &&
        (req.headers()["x-kafkito-cluster"] ?? "") !== header,
    );
    await pickCluster(page, SCRAM);
    const scramHeader = (await scramRequest).headers()["x-kafkito-cluster"];
    const scramSent = JSON.parse(atob(scramHeader)) as { auth: unknown; tls: unknown };
    expect(scramSent.auth).toEqual({
      type: "scram-sha-512",
      username: "svc-e2e",
      password: SCRAM_PASSWORD,
    });
    expect(scramSent.tls).toEqual({ enabled: true, insecure_skip_verify: true });
    await expect(page.getByText(/destination not allowed/).first()).toBeVisible({
      timeout: 20_000,
    });

    const body = (await page.locator("body").innerText()) ?? "";
    const secrets = [SCRAM_PASSWORD, SR_PASSWORD, ...new Set(headers)];
    for (const secret of secrets) {
      expect(body, "page text leaks a secret").not.toContain(secret);
      for (const line of consoleLines) expect(line, "console leaks a secret").not.toContain(secret);
    }

    // The app read the stored connections without rewriting them.
    expect(await page.evaluate((key) => window.localStorage.getItem(key), STORAGE_KEY)).toBe(
      payload,
    );
  });

  test("a connection that does not remember its password asks for it in a new tab", async ({
    page,
  }) => {
    const broker = privateClusterBroker();
    const name = "e2e-session";
    const srPassword = "e2e-session-sr-secret-pw";
    const payload = JSON.stringify([
      {
        id: "pc_5e55104e5e55104e",
        name,
        brokers: [broker],
        auth: { type: "none" },
        tls: { enabled: false, insecure_skip_verify: false },
        schema_registry: {
          url: "http://10.255.255.1:8081",
          username: "sr-user",
          credential_required: true,
        },
        remember_credentials: false,
        created_at: 1767261600000,
        updated_at: 1767261600000,
      },
    ]);
    await seed(page, payload);

    await page.goto(`/clusters/${name}/topics`);
    const dialog = page.getByRole("dialog", { name: `Connect to ${name}` });
    await expect(dialog).toBeVisible();

    const topicsRequest = page.waitForRequest(
      (req) =>
        req.method() === "GET" &&
        new URL(req.url()).pathname === "/api/v1/clusters/__private__/topics" &&
        !!req.headers()["x-kafkito-cluster"],
    );
    await dialog.getByLabel("Schema Registry password for sr-user").fill(srPassword);
    await dialog.getByRole("button", { name: "Connect" }).click();

    const sent = JSON.parse(atob((await topicsRequest).headers()["x-kafkito-cluster"])) as {
      schema_registry: { password?: string };
    };
    expect(sent.schema_registry.password).toBe(srPassword);
    await expect(page.getByRole("row", { name: new RegExp(FIXTURE_TOPIC) })).toBeVisible();
    expect(await page.evaluate((key) => window.localStorage.getItem(key), STORAGE_KEY)).toBe(
      payload,
    );
  });
});
