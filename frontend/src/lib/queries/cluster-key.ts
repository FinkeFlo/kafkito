import type { QueryClient } from "@tanstack/react-query";
import { getPrivateClusterByName } from "../private-clusters";

/**
 * Cache identity of a cluster. Shared (server-configured) clusters are keyed
 * by name; a browser-stored private cluster by its id, in an object so it can
 * never equal a shared cluster's name.
 */
export type ClusterKey = string | { readonly private: string };

/**
 * Returns the cache identity of the cluster a request for `cluster` goes to.
 * Resolves the name the way the API client does (api-client.ts): a private
 * cluster with that name takes precedence over a shared one. So a private
 * and a shared cluster with the same name never share cache entries, and
 * neither do two private clusters that held the same name at different times.
 */
export function clusterKey(cluster: string): ClusterKey {
  const priv = getPrivateClusterByName(cluster);
  return priv ? { private: priv.id } : cluster;
}

function isPrivateClusterKey(part: unknown, id: string): boolean {
  return (
    typeof part === "object" && part !== null && (part as { private?: unknown }).private === id
  );
}

/**
 * Drops every cached query of the private cluster `id`. Its entries keep
 * their key when the stored config changes (the id stays), so without this
 * an edit would keep showing data from the old brokers or credentials.
 */
export function removePrivateClusterQueries(qc: QueryClient, id: string): void {
  qc.removeQueries({ predicate: (q) => q.queryKey.some((part) => isPrivateClusterKey(part, id)) });
}

/**
 * Refetches every query of the private cluster `id`, for example once the
 * user entered its passwords and the earlier attempts failed without them.
 */
export function invalidatePrivateClusterQueries(qc: QueryClient, id: string): Promise<void> {
  return qc.invalidateQueries({
    predicate: (q) => q.queryKey.some((part) => isPrivateClusterKey(part, id)),
  });
}
