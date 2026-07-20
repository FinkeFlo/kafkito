import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createRootRoute,
  createRouter,
  createMemoryHistory,
} from "@tanstack/react-router";

// useCluster only treats a stored cluster as "selected" when it appears in the
// known cluster list, so stub the shared-cluster fetch to expose the names the
// stored-value assertions depend on. This keeps the storage signal the only
// variable under test.
vi.mock("./api", () => ({
  fetchClusters: vi.fn(async () => [
    { name: "seeded", reachable: true, is_prod: false, auth_type: "none", tls: false, schema_registry: false },
    { name: "from-other-tab", reachable: true, is_prod: false, auth_type: "none", tls: false, schema_registry: false },
    { name: "prod", reachable: true, is_prod: true, auth_type: "none", tls: false, schema_registry: false },
  ]),
}));

import { useCluster } from "./use-cluster";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

// useCluster reads route params and router state, so it must run under a
// RouterProvider. A minimal in-memory router whose root route renders its
// children is enough to satisfy the router hooks without the full app tree.
function makeWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const rootRoute = createRootRoute();
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} defaultComponent={() => <>{children}</>} />
      </QueryClientProvider>
    );
  };
}

describe("useCluster", () => {
  it("seeds the stored cluster from localStorage on first render", async () => {
    localStorage.setItem("kafkito.cluster", "seeded");

    const { result } = renderHook(() => useCluster(), { wrapper: makeWrapper() });

    // The stored name is honoured once the known-cluster list has loaded.
    await waitFor(() => expect(result.current.cluster).toBe("seeded"));
  });

  it("re-reads the stored cluster when another tab dispatches a kafkito storage event", async () => {
    localStorage.setItem("kafkito.cluster", "seeded");
    const { result } = renderHook(() => useCluster(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.cluster).toBe("seeded"));

    act(() => {
      localStorage.setItem("kafkito.cluster", "from-other-tab");
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "kafkito.cluster",
          oldValue: "seeded",
          newValue: "from-other-tab",
        }),
      );
    });

    expect(result.current.cluster).toBe("from-other-tab");
  });

  it("ignores storage events for unrelated keys even when localStorage has drifted", async () => {
    localStorage.setItem("kafkito.cluster", "seeded");
    const { result } = renderHook(() => useCluster(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.cluster).toBe("seeded"));

    act(() => {
      // localStorage drifts to another known cluster, but the event fires for
      // an unrelated key — the filter must skip and keep the previous value.
      localStorage.setItem("kafkito.cluster", "from-other-tab");
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "some.other.key",
          oldValue: null,
          newValue: "anything",
        }),
      );
    });

    expect(result.current.cluster).toBe("seeded");
  });
});
