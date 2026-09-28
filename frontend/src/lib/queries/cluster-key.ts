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
