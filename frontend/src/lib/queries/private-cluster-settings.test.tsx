import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider, type QueryKey } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import type { PrivateCluster } from "@/lib/private-clusters";
import { Route as SettingsRoute } from "@/routes/settings.clusters";
import { groupQueries } from "./groups";
import { messageQueries } from "./messages";
import { topicQueries } from "./topics";

// The private-cluster settings page: names that collide with a server
// cluster are refused (and flagged on entries saved before), and editing or
// deleting a private cluster drops its cached queries.

const fetchClusters = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchClusters };
});

const SHARED = "local";
const STORAGE_KEY = "kafkito.private-clusters.v1";
const HINT = "Same name as a server cluster – rename it to reach both";

function privateCluster(id: string, name: string): PrivateCluster {
  return {
    id,
    name,
    brokers: ["10.0.0.1:9092"],
    auth: { type: "none" },
    tls: { enabled: false },
    created_at: 0,
    updated_at: 0,
  };
}

function store(...clusters: PrivateCluster[]) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(clusters));
}

function renderSettings(qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
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
  return qc;
}

function row(name: string) {
  return screen.getByRole("row", { name: new RegExp(`Select ${name} for export`) });
}

beforeEach(() => {
  fetchClusters.mockReset().mockResolvedValue([
    {
      name: SHARED,
      reachable: true,
      is_prod: false,
      auth_type: "none",
      tls: false,
      schema_registry: false,
    },
  ]);
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("private cluster settings", () => {
  it("refuses a name that a server cluster already has", async () => {
    const user = userEvent.setup();
    renderSettings();
    await waitFor(() => expect(fetchClusters).toHaveBeenCalled());

    await user.click(screen.getByRole("button", { name: "Add cluster" }));
    const form = await screen.findByRole("dialog", { name: "Add private cluster" });
    const name = within(form).getByPlaceholderText("my-dev-cluster");
    await user.type(within(form).getByPlaceholderText("host1:9092, host2:9092"), "10.0.0.1:9092");
    await user.type(name, ` ${SHARED} `);

    const error = await within(form).findByText(
      `A server cluster is already named "${SHARED}". Choose another name.`,
    );
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name.getAttribute("aria-describedby")).toBe(error.closest("p")?.id);
    expect(error.closest("[aria-live]")).toHaveAttribute("aria-live", "polite");
    expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled();

    await user.type(name, "-2");
    expect(within(form).queryByText(/already named/)).not.toBeInTheDocument();
    expect(name).not.toHaveAttribute("aria-invalid");
    expect(within(form).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("flags a stored private cluster that collides, until it is renamed", async () => {
    const user = userEvent.setup();
    store(privateCluster("pc_old", SHARED), privateCluster("pc_ok", "mine"));
    renderSettings();

    await screen.findByRole("row", { name: new RegExp(`Select ${SHARED} for export`) });
    expect(await within(row(SHARED)).findByText(HINT)).toBeInTheDocument();
    expect(within(row(SHARED)).getByRole("img", { name: "Warning" })).toBeInTheDocument();
    expect(within(row("mine")).queryByText(HINT)).not.toBeInTheDocument();

    await user.click(within(row(SHARED)).getByRole("button", { name: "Edit" }));
    const form = await screen.findByRole("dialog", { name: "Edit private cluster" });
    expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled();
    const name = within(form).getByPlaceholderText("my-dev-cluster");
    await user.clear(name);
    await user.type(name, "local-private");
    await user.click(within(form).getByRole("button", { name: "Save" }));

    expect(
      await screen.findByRole("row", { name: /Select local-private for export/ }),
    ).toBeVisible();
    expect(screen.queryByText(HINT)).not.toBeInTheDocument();
  });

  it("drops the cached queries of a private cluster when it is edited or deleted", async () => {
    const user = userEvent.setup();
    store(privateCluster("pc_a", "a"), privateCluster("pc_b", "b"));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const keysFor = (cluster: string): QueryKey[] => [
      topicQueries.list(cluster).queryKey,
      groupQueries.list(cluster).queryKey,
      messageQueries.page(cluster, "t", {}).queryKey,
    ];
    const [a, b, shared] = [keysFor("a"), keysFor("b"), keysFor(SHARED)];
    for (const key of [...a, ...b, ...shared]) qc.setQueryData(key, "cached");
    renderSettings(qc);

    await user.click(
      within(await screen.findByRole("row", { name: /Select a for export/ })).getByRole("button", {
        name: "Edit",
      }),
    );
    const form = await screen.findByRole("dialog", { name: "Edit private cluster" });
    const brokers = within(form).getByPlaceholderText("host1:9092, host2:9092");
    await user.clear(brokers);
    await user.type(brokers, "10.0.0.2:9092");
    await user.click(within(form).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    for (const key of a) expect(qc.getQueryData(key)).toBeUndefined();
    for (const key of [...b, ...shared]) expect(qc.getQueryData(key)).toBe("cached");

    await user.click(within(row("b")).getByRole("button", { name: "Delete" }));
    const confirm = await screen.findByRole("dialog", { name: "Delete private cluster?" });
    await user.click(within(confirm).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(qc.getQueryData(b[0])).toBeUndefined());
    for (const key of b) expect(qc.getQueryData(key)).toBeUndefined();
    for (const key of shared) expect(qc.getQueryData(key)).toBe("cached");
  });
});
