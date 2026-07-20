import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import type { ClusterListItem } from "@/lib/use-cluster";

type UseClusterReturn = {
  cluster: string | null;
  clusters: ClusterListItem[] | undefined;
  setCluster: (name: string) => void;
  isLoading: boolean;
};

let mockState: UseClusterReturn;

vi.mock("@/lib/use-cluster", () => ({
  useCluster: () => mockState,
}));

import { ClusterPill } from "./ClusterPill";

const loadedCluster: ClusterListItem = {
  name: "local",
  source: "shared",
  reachable: true,
  is_prod: false,
  tls: false,
  auth_type: "none",
  schema_registry: false,
};

describe("ClusterPill hook-order regression (React #310)", () => {
  // Reproduces the crash where a useEffect declared after the loading/no-cluster
  // early returns ran only once clusters arrived, changing the hook count between
  // renders ("Rendered more hooks than during the previous render") and tripping
  // the global error boundary on every cluster page.
  it("does not crash when transitioning from loading to a loaded cluster list", () => {
    mockState = {
      cluster: null,
      clusters: undefined,
      setCluster: vi.fn(),
      isLoading: true,
    };
    const { rerender } = render(<ClusterPill />);

    mockState = {
      cluster: "local",
      clusters: [loadedCluster],
      setCluster: vi.fn(),
      isLoading: false,
    };

    expect(() => rerender(<ClusterPill />)).not.toThrow();
  });
});
