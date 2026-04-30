import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useRouterState, useSearch } from "@tanstack/react-router";
import { fetchClusters, type ClusterInfo } from "./api";
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
  const search = useSearch({ strict: false }) as { cluster?: string };
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

  const fromUrl = search.cluster;
  const stored = readStored();

  const cluster = useMemo<string | null>(() => {
    if (fromUrl && known(fromUrl)) return fromUrl;
    if (known(stored)) return stored;
    return defaultCluster;
  }, [fromUrl, stored, known, defaultCluster]);

  const isUnknownCluster = !!fromUrl && !!clusters && !known(fromUrl);

  // Reflect resolved cluster into URL once data arrives so deep-links and
  // shareable URLs keep working without forcing every route to set it manually.
  useEffect(() => {
    if (!clusters || !cluster) return;
    if (fromUrl === cluster) return;
    if (location.pathname === "/preview") return;
    navigate({
      to: location.pathname,
      search: (prev: Record<string, unknown>) => ({ ...prev, cluster }),
      replace: true,
    });
  }, [cluster, clusters, fromUrl, navigate, location.pathname]);

  // Persist any explicit URL value so reload restores it across tabs.
  useEffect(() => {
    if (cluster && cluster !== stored) writeStored(cluster);
  }, [cluster, stored]);

  const setCluster = useCallback(
    (name: string) => {
      writeStored(name);
      // Detail-routes carry a path-param resource that may not exist on the
      // target cluster; bounce back to the matching list page.
      const path = location.pathname;
      const isTopicDetail = /^\/topics\/[^/]+/.test(path);
      const isGroupDetail = /^\/groups\/[^/]+/.test(path);
      if (isTopicDetail) {
        navigate({ to: "/topics", search: { cluster: name } });
        return;
      }
      if (isGroupDetail) {
        navigate({ to: "/groups", search: { cluster: name, group: undefined } });
        return;
      }
      navigate({
        to: path,
        search: (prev: Record<string, unknown>) => ({ ...prev, cluster: name }),
        replace: true,
      });
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
