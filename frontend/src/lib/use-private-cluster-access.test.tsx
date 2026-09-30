import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import { AuthProvider } from "@/auth/AuthProvider";
import type { Me } from "@/auth/types";
import type { PrivateCluster } from "@/lib/private-clusters";
import { authQueries } from "@/lib/queries/auth";
import { useCluster } from "@/lib/use-cluster";
import { usePrivateClusterAccess } from "@/lib/use-private-cluster-access";
import { Route as ClustersIndexRoute } from "@/routes/clusters.index";

// Private clusters stay in localStorage but drop out of cluster selection
// (the switcher list from useCluster and the fleet overview) while
// GET /api/v1/me reports that the caller may not use them.

const fetchClusters = vi.hoisted(() => vi.fn());
const apiFetch = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchClusters };
});

vi.mock("@/auth/api", () => ({ apiFetch }));

const STORAGE_KEY = "kafkito.private-clusters.v1";

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

function client(privateClusters?: Me["private_clusters"]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (privateClusters) qc.setQueryData(authQueries.me().queryKey, meWith(privateClusters));
  return qc;
}

function queryWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

// useCluster reads router state, so the hook runs under a minimal router.
function routerWrapper(qc: QueryClient) {
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={qc}>
        <RouterProvider router={router} defaultComponent={() => <>{children}</>} />
      </QueryClientProvider>
    );
  };
}

function renderOverview(qc: QueryClient) {
  const root = createRootRoute();
  const overview = ClustersIndexRoute.update({
    id: "/clusters/",
    path: "/clusters/",
    getParentRoute: () => root,
    // Same cast as routeTree.gen.ts: update() is typed for generated trees.
  } as any);
  const router = createRouter({
    routeTree: root.addChildren([overview]),
    history: createMemoryHistory({ initialEntries: ["/clusters/"] }),
  });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  fetchClusters.mockReset().mockResolvedValue([
    {
      name: "shared",
      reachable: true,
      is_prod: false,
      auth_type: "none",
      tls: false,
      schema_registry: false,
    },
  ]);
  apiFetch.mockReset().mockRejectedValue(new Error("unexpected /me fetch"));
  localStorage.setItem(STORAGE_KEY, JSON.stringify([saved]));
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("usePrivateClusterAccess", () => {
  it.each([
    [
      { mode: "on", allowed: true },
      { mode: "on", allowed: true },
    ],
    [
      { mode: "off", allowed: false },
      { mode: "off", allowed: false },
    ],
    [
      { mode: "role", allowed: false },
      { mode: "role", allowed: false },
    ],
    [
      { mode: "role", allowed: true },
      { mode: "role", allowed: true },
    ],
  ] as const)("reports %o from /me", (status, want) => {
    const { result } = renderHook(() => usePrivateClusterAccess(), {
      wrapper: queryWrapper(client(status)),
    });
    expect(result.current).toEqual(want);
  });

  it("counts as allowed until /me has loaded, without requesting it", () => {
    const { result } = renderHook(() => usePrivateClusterAccess(), {
      wrapper: queryWrapper(client()),
    });
    expect(result.current).toEqual({ allowed: true, mode: undefined });
    expect(apiFetch).not.toHaveBeenCalled();
  });

  it("follows the /me response that AuthProvider loads", async () => {
    apiFetch.mockImplementation(async (url: string) => {
      if (url !== "/api/v1/me") throw new Error(`unexpected ${url}`);
      return new Response(JSON.stringify(meWith({ mode: "off", allowed: false })), {
        headers: { "Content-Type": "application/json" },
      });
    });
    const qc = client();
    const { result } = renderHook(() => usePrivateClusterAccess(), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={qc}>
          <AuthProvider>{children}</AuthProvider>
        </QueryClientProvider>
      ),
    });

    await waitFor(() => expect(result.current).toEqual({ allowed: false, mode: "off" }));
    // Only AuthProvider requests /me.
    expect(apiFetch.mock.calls.filter(([url]) => url === "/api/v1/me")).toHaveLength(1);
  });
});

describe("cluster selection", () => {
  it.each([
    ["off", false, ["shared"]],
    ["role", false, ["shared"]],
    ["role", true, ["shared", "mine"]],
    ["on", true, ["shared", "mine"]],
  ] as const)(
    "lists private clusters in the switcher for mode %s, allowed %s",
    async (mode, allowed, names) => {
      const before = localStorage.getItem(STORAGE_KEY);
      const { result } = renderHook(() => useCluster(), {
        wrapper: routerWrapper(client({ mode, allowed })),
      });

      await waitFor(() => expect(result.current.clusters?.map((c) => c.name)).toEqual(names));
      expect(localStorage.getItem(STORAGE_KEY)).toBe(before);
    },
  );

  it.each([
    [false, false],
    [true, true],
  ] as const)(
    "lists private clusters in the fleet overview when allowed is %s",
    async (allowed, listed) => {
      renderOverview(client({ mode: allowed ? "on" : "off", allowed }));

      expect(await screen.findByText("shared")).toBeInTheDocument();
      if (listed) {
        expect(screen.getByText("mine")).toBeInTheDocument();
        expect(screen.getByText("PRIVATE")).toBeInTheDocument();
      } else {
        expect(screen.queryByText("mine")).not.toBeInTheDocument();
        expect(screen.queryByText("PRIVATE")).not.toBeInTheDocument();
      }
    },
  );
});
