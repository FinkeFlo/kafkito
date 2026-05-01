import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { fetchClusters, type ClusterInfo } from "./api";
import { computeSwitchTarget } from "./cluster-switch";
import {
  listPrivateClusters,
  subscribePrivateClusters,
  type PrivateCluster,
} from "./private-clusters";

const STORAGE_KEY = "kafkito.cluster";

export type ClusterSource = "shared" | "private";

export interface ClusterListItem extends ClusterInfo {
  source: ClusterSource;
  /** Present only when source === "private". */
  id?: string;
}

function privateToListItem(p: PrivateCluster): ClusterListItem {
  return {
    source: "private",
    id: p.id,
    name: p.name,
    reachable: true, // optimistic; settings page probes on demand
    auth_type: p.auth.type,
    tls: !!p.tls.enabled,
    schema_registry: !!p.schema_registry?.url,
  };
}

function sharedToListItem(c: ClusterInfo): ClusterListItem {
  return { ...c, source: "shared" };
}

function usePrivateClusters(): PrivateCluster[] {
  const [items, setItems] = useState<PrivateCluster[]>(() =>
    typeof window === "undefined" ? [] : listPrivateClusters(),
  );
  useEffect(() => {
    const unsub = subscribePrivateClusters(() => {
      setItems(listPrivateClusters());
    });
    // In case storage changed between SSR snapshot and mount.
    setItems(listPrivateClusters());
    return unsub;
  }, []);
  return items;
}

function readStored(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeStored(name: string) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, name);
  } catch {
    /* ignore */
  }
}

interface UseClusterResult {
  cluster: string | null;
  clusters: ClusterListItem[] | undefined;
  setCluster: (name: string) => void;
  isLoading: boolean;
  isUnknownCluster: boolean;
  defaultCluster: string | null;
}

export function useCluster(): UseClusterResult {
  // Cluster now lives in the URL path under /clusters/$cluster/...; read it
  // from the loose-typed merged params so the hook works from any descendant
  // route. URL-decode so consumers see the raw cluster name.
  const params = useParams({ strict: false }) as { cluster?: string };
  const navigate = useNavigate();
  const location = useRouterState({ select: (s) => s.location });

  const clustersQuery = useQuery({
    queryKey: ["clusters"],
    queryFn: fetchClusters,
    refetchInterval: 10_000,
    staleTime: 5_000,
  });

  const privateClusters = usePrivateClusters();

  const clusters = useMemo<ClusterListItem[] | undefined>(() => {
    const shared = clustersQuery.data?.map(sharedToListItem) ?? [];
    const priv = privateClusters.map(privateToListItem);
    // Dedupe: if a private cluster happens to share its name with a shared
    // cluster, the shared entry wins and the private one is dropped from the
    // switcher (the user can rename).
    const seen = new Set(shared.map((c) => c.name));
    const merged = [...shared, ...priv.filter((c) => !seen.has(c.name))];
    if (!clustersQuery.data && priv.length === 0) return undefined;
    return merged;
  }, [clustersQuery.data, privateClusters]);

  const defaultCluster = useMemo(() => {
    if (!clusters || clusters.length === 0) return null;
    const reachable = clusters.find((c) => c.reachable);
    return (reachable ?? clusters[0]).name;
  }, [clusters]);

  const known = useCallback(
    (name: string | null | undefined): name is string => {
      if (!name || !clusters) return false;
      return clusters.some((c) => c.name === name);
    },
    [clusters],
  );

  const fromUrl = params.cluster ? decodeURIComponent(params.cluster) : undefined;
  const stored = readStored();

  const cluster = useMemo<string | null>(() => {
    if (fromUrl && known(fromUrl)) return fromUrl;
    if (known(stored)) return stored;
    return defaultCluster;
  }, [fromUrl, stored, known, defaultCluster]);

  const isUnknownCluster = !!fromUrl && !!clusters && !known(fromUrl);

  // Persist any explicit URL value so reload restores it across tabs.
  useEffect(() => {
    if (cluster && cluster !== stored) writeStored(cluster);
  }, [cluster, stored]);

  const setCluster = useCallback(
    (name: string) => {
      writeStored(name);
      // computeSwitchTarget returns a fully-formed `/clusters/<name>/<section>`
      // pathname (URL-encoded, no query). The typed router accepts string
      // targets at runtime — cast since the literal isn't in the route union.
      const target = computeSwitchTarget(location.pathname, name);
      navigate({ to: target as never });
    },
    [navigate, location.pathname],
  );

  return {
    cluster,
    clusters,
    setCluster,
    isLoading: clustersQuery.isLoading,
    isUnknownCluster,
    defaultCluster,
  };
}
