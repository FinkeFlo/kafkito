import type { Page, Request } from "@playwright/test";
import { test, expect, SECOND_CLUSTER_NAME, SR_CLUSTER_NAME } from "./fixtures/private-cluster";

// `local` (fixtures/kafkito-e2e.yaml) points at the e2e Schema Registry that
// seed.sh fills; the capability checks for a cluster without a registry use
// the private cluster the `page` fixture stores (SECOND_CLUSTER_NAME, no
// schema_registry).
const PRIMARY = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const c = encodeURIComponent(PRIMARY);

// Fixture coupling: seed.sh's seed_schema_registry.
const AVRO_SUBJECT = "e2e-avro-orders-value";
const AVRO_TOPIC = "e2e-avro-orders";
const JSON_SUBJECT = "e2e-json-customers-value";
const PROTO_SUBJECT = "e2e-proto-events-value";
// Registered and deleted by the delete walk below; seed.sh resets it too.
const DELETE_SUBJECT = "e2e-delete-me-value";

const subjectsPath = `/api/v1/clusters/${c}/schemas/subjects`;
const versionPath = (subject: string, version: string | number) =>
  `${subjectsPath}/${encodeURIComponent(subject)}/versions/${version}`;

function subjectRow(page: Page, subject: string) {
  return page.getByRole("listitem").filter({
    has: page.getByRole("button", { name: new RegExp(`^${subject}`) }),
  });
}

function subjectButton(page: Page, subject: string) {
  return page.getByRole("button", { name: new RegExp(`^${subject}`) });
}

function schemaText(page: Page) {
  return page.getByRole("main").locator("pre");
}

// Counts GET requests per API path, so a walk can assert that a resource
// was fetched exactly once while it moved between pages.
function countRequests(page: Page) {
  const counts = new Map<string, number>();
  page.on("request", (req: Request) => {
    if (req.method() !== "GET") return;
    const path = new URL(req.url()).pathname;
    if (!path.startsWith("/api/")) return;
    counts.set(path, (counts.get(path) ?? 0) + 1);
  });
  return (path: string) => counts.get(path) ?? 0;
}

test.describe("Schemas tab (capability-driven)", () => {
  test("schemas link shows '(—)' suffix and aria-disabled when active cluster has no SR", async ({
    page,
  }) => {
    await page.goto(`/clusters/${encodeURIComponent(SECOND_CLUSTER_NAME)}/topics`);

    const schemasLink = page.getByRole("link", { name: /^Schemas/ });
    await expect(schemasLink).toBeVisible();
    await expect(schemasLink).toContainText(/\(—\)/);
    await expect(schemasLink).toHaveAttribute("aria-disabled", "true");
  });

  test("schemas landing page shows 'Schemas not configured' notice for cluster without SR", async ({
    page,
  }) => {
    await page.goto(`/clusters/${encodeURIComponent(SECOND_CLUSTER_NAME)}/schemas`);

    await expect(page.getByRole("heading", { level: 1, name: "Schemas" })).toBeVisible();
    await expect(page.getByText(/Schemas not configured/i)).toBeVisible();
    await expect(page.getByRole("button", { name: /^\+ Register schema$/ })).toBeDisabled();
  });

  test("schemas link omits '(—)' suffix and is not aria-disabled when active cluster has SR", async ({
    pageWithSRCluster,
  }) => {
    await pageWithSRCluster.goto(`/clusters/${encodeURIComponent(SR_CLUSTER_NAME)}/topics`);

    const schemasLink = pageWithSRCluster.getByRole("link", {
      name: "Schemas",
      exact: true,
    });
    await expect(schemasLink).toBeVisible();
    await expect(schemasLink).not.toHaveAttribute("aria-disabled", "true");
  });

  test("schemas landing page on SR-enabled cluster renders subjects-list filter, not the not-configured notice", async ({
    pageWithSRCluster,
  }) => {
    await pageWithSRCluster.goto(`/clusters/${encodeURIComponent(SR_CLUSTER_NAME)}/schemas`);

    await expect(
      pageWithSRCluster.getByRole("heading", { level: 1, name: "Schemas" }),
    ).toBeVisible();
    await expect(pageWithSRCluster.getByText(/Schemas not configured/i)).toBeHidden();
    await expect(
      pageWithSRCluster.getByRole("textbox", { name: /filter subjects/i }),
    ).toBeVisible();
  });

  test("the e2e cluster's schemas link is enabled", async ({ page }) => {
    await page.goto(`/clusters/${c}/topics`);

    const schemasLink = page.getByRole("link", { name: "Schemas", exact: true });
    await expect(schemasLink).toBeVisible();
    await expect(schemasLink).not.toHaveAttribute("aria-disabled", "true");
  });
});

test.describe("Schemas page (seeded Schema Registry)", () => {
  test("lists the seeded subjects with type and versions, and the filter narrows the list", async ({
    page,
  }) => {
    await page.goto(`/clusters/${c}/schemas`);
    await expect(page.getByRole("heading", { level: 1, name: "Schemas" })).toBeVisible();
    await expect(page.getByText(/Schemas not configured/i)).toBeHidden();

    const avro = subjectRow(page, AVRO_SUBJECT);
    await expect(avro).toBeVisible();
    await expect(avro).toContainText("AVRO");
    await expect(avro).toContainText("v2");
    await expect(avro).toContainText("2 versions");

    const json = subjectRow(page, JSON_SUBJECT);
    await expect(json).toBeVisible();
    await expect(json).toContainText("JSON");
    await expect(json).toContainText("v1");

    const proto = subjectRow(page, PROTO_SUBJECT);
    await expect(proto).toBeVisible();
    await expect(proto).toContainText("PROTOBUF");
    await expect(proto).toContainText("v1");

    await expect(page.getByText("No subject selected")).toBeVisible();

    const filter = page.getByRole("textbox", { name: "Filter subjects" });
    await filter.fill("json-customers");
    await expect(json).toBeVisible();
    await expect(avro).toBeHidden();
    await expect(proto).toBeHidden();
    await expect(page.getByText(/^1 of \d+$/)).toBeVisible();

    await filter.fill("no-such-e2e-subject-xyz");
    await expect(page.getByText("No subjects match your filter.")).toBeVisible();
    await expect(json).toBeHidden();

    await filter.fill("");
    await expect(avro).toBeVisible();
    await expect(json).toBeVisible();
    await expect(proto).toBeVisible();
  });

  test("opening a subject shows its schema, compatibility and version; the version param switches the schema", async ({
    page,
  }) => {
    await page.goto(`/clusters/${c}/schemas`);

    await subjectButton(page, AVRO_SUBJECT).click();
    await expect(page).toHaveURL(new RegExp(`subject=${AVRO_SUBJECT}&version=latest`));
    await expect(subjectButton(page, AVRO_SUBJECT)).toHaveAttribute("aria-current", "true");
    await expect(page.getByText("Latest: v2")).toBeVisible();
    await expect(page.getByText("Compatibility: BACKWARD")).toBeVisible();
    await expect(schemaText(page)).toContainText('"name": "currency"');
    await expect(schemaText(page)).toContainText('"name": "note"');

    // The page has no version picker; a version is selected through the
    // `version` search param (deep link, back/forward).
    await page.goto(`/clusters/${c}/schemas?subject=${AVRO_SUBJECT}&version=1`);
    await expect(schemaText(page)).toContainText('"name": "amount_cents"');
    await expect(schemaText(page)).not.toContainText("currency");

    // Clicking the subject goes back to its latest version.
    await subjectButton(page, AVRO_SUBJECT).click();
    await expect(page).toHaveURL(new RegExp(`subject=${AVRO_SUBJECT}&version=latest`));
    await expect(schemaText(page)).toContainText('"name": "currency"');

    // The router re-serialises the validated param as a JSON string ("1").
    await page.goBack();
    await expect(page).toHaveURL(new RegExp(`subject=${AVRO_SUBJECT}&version=(1|%221%22)$`));
    await expect(schemaText(page)).not.toContainText("currency");

    await subjectButton(page, JSON_SUBJECT).click();
    await expect(page.getByText("Compatibility: FULL")).toBeVisible();
    await expect(page.getByText("Latest: v1")).toBeVisible();
    await expect(schemaText(page)).toContainText('"title": "Customer"');
    await expect(schemaText(page)).toContainText('"customer_id"');

    // Protobuf schemas are not JSON, so they are shown as registered.
    await subjectButton(page, PROTO_SUBJECT).click();
    await expect(page.getByText("Compatibility: NONE")).toBeVisible();
    await expect(schemaText(page)).toContainText("message Event {");
    await expect(schemaText(page)).toContainText("string event_id = 1;");
  });

  test("the topic Schema tab shows the topic's subject and shares the Schemas page's cache", async ({
    page,
  }) => {
    const requests = countRequests(page);
    let loads = 0;
    page.on("load", () => loads++);

    await page.goto(`/clusters/${c}/schemas?subject=${AVRO_SUBJECT}&version=latest`);
    await expect(page.getByText("Compatibility: BACKWARD")).toBeVisible();
    await expect(schemaText(page)).toContainText('"name": "currency"');

    // Client-side navigation only: a page load would drop the query cache.
    await page.getByRole("link", { name: "Topics", exact: true }).click();
    await page.getByRole("link", { name: AVRO_TOPIC, exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name: AVRO_TOPIC })).toBeVisible();
    await page
      .getByRole("navigation", { name: "Topic sections" })
      .getByRole("link", { name: "Schema", exact: true })
      .click();

    const version = (await (
      await page.request.get(versionPath(AVRO_SUBJECT, "latest"))
    ).json()) as {
      id: number;
    };
    const tab = page.getByRole("main");
    await expect(tab.getByText(AVRO_SUBJECT, { exact: true })).toBeVisible();
    await expect(tab.getByText(`v2 · id ${version.id}`)).toBeVisible();
    await expect(tab.getByText("· BACKWARD")).toBeVisible();
    await expect(schemaText(page)).toContainText('"name":"currency"');
    await expect(tab.getByRole("link", { name: "Open full view" })).toBeVisible();

    // And back to the Schemas page, which renders from the same entries.
    await page.getByRole("link", { name: "Schemas", exact: true }).click();
    await subjectButton(page, AVRO_SUBJECT).click();
    await expect(page.getByText("Compatibility: BACKWARD")).toBeVisible();
    await expect(schemaText(page)).toContainText('"name": "currency"');

    expect(loads).toBe(1);
    expect(requests(subjectsPath)).toBe(1);
    expect(requests(versionPath(AVRO_SUBJECT, "latest"))).toBe(1);
  });

  test("the Schema tab of a topic without a subject says so", async ({ page }) => {
    await page.goto(`/clusters/${c}/topics/e2e-walk-target/schema`);

    await expect(page.getByText("No schema registered for this topic")).toBeVisible();
    await expect(page.getByText("e2e-walk-target-value")).toBeVisible();
  });

  test("deleting a subject removes it from the list without a reload", async ({ page }) => {
    // Retry-safe: a previous attempt may have left the subject soft-deleted.
    await page.request.delete(`${subjectsPath}/${DELETE_SUBJECT}`);
    await page.request.delete(`${subjectsPath}/${DELETE_SUBJECT}?permanent=true`);
    const registered = await page.request.post(`${subjectsPath}/${DELETE_SUBJECT}/versions`, {
      data: {
        schemaType: "AVRO",
        schema: JSON.stringify({
          type: "record",
          name: "DeleteMe",
          namespace: "kafkito.e2e",
          fields: [{ name: "id", type: "string" }],
        }),
      },
    });
    expect(registered.ok()).toBe(true);

    await page.goto(`/clusters/${c}/schemas`);
    let loads = 0;
    page.on("load", () => loads++);

    await subjectButton(page, DELETE_SUBJECT).click();
    await expect(page.getByText(/"name": "DeleteMe"/)).toBeVisible();

    const row = subjectRow(page, DELETE_SUBJECT);
    await row.hover();
    await row.getByRole("button", { name: `Delete subject ${DELETE_SUBJECT}` }).click();

    const dialog = page.getByRole("dialog", { name: `Delete subject "${DELETE_SUBJECT}"?` });
    await expect(dialog).toBeVisible();
    const confirm = dialog.getByRole("button", { name: "Delete subject", exact: true });
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox").fill(DELETE_SUBJECT);
    await expect(confirm).toBeEnabled();

    // The list is fresh for 30 s (staleTime) and never polls, so only the
    // mutation's invalidation can refetch it this quickly.
    const deleted = page.waitForResponse(
      (res) =>
        res.request().method() === "DELETE" &&
        new URL(res.url()).pathname === `${subjectsPath}/${DELETE_SUBJECT}`,
    );
    const refetch = page.waitForRequest(
      (req) => req.method() === "GET" && new URL(req.url()).pathname === subjectsPath,
    );
    await confirm.click();
    expect((await deleted).ok()).toBe(true);
    await refetch;

    await expect(dialog).toBeHidden();
    await expect(row).toHaveCount(0);
    await expect(page.getByText("No subject selected")).toBeVisible();
    await expect(page).not.toHaveURL(/subject=/);
    await expect(subjectRow(page, AVRO_SUBJECT)).toBeVisible();
    expect(loads).toBe(0);

    const gone = await page.request.get(`${subjectsPath}/${DELETE_SUBJECT}/versions`);
    expect(gone.ok()).toBe(false);
  });
});
