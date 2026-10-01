import type { Page } from "@playwright/test";

export const UNREACHABLE_CLUSTER = "e2e-down";

/**
 * Appends an unreachable shared cluster to the real cluster list, so the
 * healthy and unhealthy indicators render side by side. e2e only runs one
 * broker, so the down state can't be produced for real.
 */
export async function withUnreachableCluster(page: Page): Promise<void> {
  await page.route("**/api/v1/clusters", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    body.clusters.push({
      name: UNREACHABLE_CLUSTER,
      reachable: false,
      error: "connection refused",
      error_class: "refused",
      is_prod: false,
      auth_type: "none",
      tls: false,
      schema_registry: false,
    });
    await route.fulfill({ response: res, json: body });
  });
}
