import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import type { Me } from "@/auth/types";
import type { PrivateCluster } from "@/lib/private-clusters";
import { authQueries } from "@/lib/queries/auth";
import { Route as SettingsRoute } from "@/routes/settings.clusters";

// The private-cluster settings page follows `private_clusters` from
// GET /api/v1/me. When the server does not allow them, a notice names the
// reason and the actions that reach a cluster (add, import, test connection)
// are disabled; entries already stored in the browser stay untouched.

const fetchClusters = vi.hoisted(() => vi.fn());
const testCluster = vi.hoisted(() => vi.fn());
const apiFetch = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchClusters, testCluster };
});

vi.mock("@/auth/api", () => ({ apiFetch }));

const STORAGE_KEY = "kafkito.private-clusters.v1";
const DISABLED = "Private clusters are disabled on this server.";
const FORBIDDEN = "Your role does not allow private clusters.";

const saved: PrivateCluster = {
  id: "pc_1",
  name: "mine",
  brokers: ["10.0.0.1:9092"],
  auth: { type: "none" },
  tls: { enabled: false },
  created_at: 0,
  updated_at: 0,
};

function meWith(privateClusters: Me["private_clusters"]): Me {
  return {
    user: "dev-user",
    email: "",
    tenant: "",
    scopes: null,
    roles: null,
    permissions: {},
    anonymous: false,
    jwt: false,
    rbac_enabled: privateClusters.mode === "role",
    private_clusters: privateClusters,
  };
}

function renderSettings(privateClusters?: Me["private_clusters"]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (privateClusters) qc.setQueryData(authQueries.me().queryKey, meWith(privateClusters));
  const root = createRootRoute();
  const settings = SettingsRoute.update({
    id: "/settings/clusters",
    path: "/settings/clusters",
    getParentRoute: () => root,
    // Same cast as routeTree.gen.ts: update() is typed for generated trees.
  } as any);
  const router = createRouter({
    routeTree: root.addChildren([settings]),
    history: createMemoryHistory({ initialEntries: ["/settings/clusters"] }),
  });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  fetchClusters.mockReset().mockResolvedValue([]);
  testCluster.mockReset();
  apiFetch.mockReset().mockRejectedValue(new Error("unexpected /me fetch"));
  localStorage.setItem(STORAGE_KEY, JSON.stringify([saved]));
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("private cluster settings when the server does not allow them", () => {
  it.each([
    ["off", DISABLED],
    ["role", FORBIDDEN],
  ] as const)(
    "explains mode %s and disables add, import and test connection",
    async (mode, reason) => {
      const user = userEvent.setup();
      const before = localStorage.getItem(STORAGE_KEY);
      renderSettings({ mode, allowed: false });

      expect(await screen.findByRole("alert")).toHaveTextContent(reason);
      for (const name of ["Add cluster", "Import JSON"]) {
        const button = screen.getByRole("button", { name });
        expect(button).toBeDisabled();
        expect(button).toHaveAccessibleDescription(reason);
      }
      expect(screen.getByRole("button", { name: "Export JSON" })).toBeEnabled();

      const row = screen.getByRole("row", { name: /Select mine for export/ });
      expect(within(row).getByRole("button", { name: "Delete" })).toBeEnabled();
      await user.click(within(row).getByRole("button", { name: "Edit" }));
      const form = await screen.findByRole("dialog", { name: "Edit private cluster" });
      const test = within(form).getByRole("button", { name: "Test connection" });
      expect(test).toBeDisabled();
      expect(test).toHaveAccessibleDescription(reason);
      expect(within(form).getByText(reason)).toBeVisible();
      expect(within(form).getByRole("button", { name: "Save" })).toBeEnabled();

      expect(testCluster).not.toHaveBeenCalled();
      expect(apiFetch).not.toHaveBeenCalled();
      expect(localStorage.getItem(STORAGE_KEY)).toBe(before);
    },
  );
});

describe("private cluster settings when the server allows them", () => {
  it.each(["on", "role"] as const)("offers every action in mode %s", async (mode) => {
    const user = userEvent.setup();
    renderSettings({ mode, allowed: true });

    const add = await screen.findByRole("button", { name: "Add cluster" });
    expect(add).toBeEnabled();
    expect(add).not.toHaveAttribute("aria-describedby");
    expect(screen.getByRole("button", { name: "Import JSON" })).toBeEnabled();
    expect(screen.queryByText(DISABLED)).not.toBeInTheDocument();
    expect(screen.queryByText(FORBIDDEN)).not.toBeInTheDocument();

    await user.click(add);
    const form = await screen.findByRole("dialog", { name: "Add private cluster" });
    await user.type(within(form).getByPlaceholderText("my-dev-cluster"), "dev");
    await user.type(within(form).getByPlaceholderText("host1:9092, host2:9092"), "10.0.0.2:9092");
    expect(within(form).getByRole("button", { name: "Test connection" })).toBeEnabled();
  });

  it("keeps the actions available until /me has loaded", async () => {
    renderSettings();

    expect(await screen.findByRole("button", { name: "Add cluster" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Import JSON" })).toBeEnabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(apiFetch).not.toHaveBeenCalled();
  });
});
