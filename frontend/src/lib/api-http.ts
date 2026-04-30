// Low-level HTTP helper that injects the X-Kafkito-Cluster header for
// private (browser-stored) clusters. All cluster-scoped API functions go
// through clusterPath() to pick the right URL segment and fetchAPI() to
// attach the header.

import { apiFetch } from "../auth/api";
import {
  PRIVATE_CLUSTER_SENTINEL,
  encodePrivateClusterHeader,
  getPrivateClusterByName,
} from "./private-clusters";

/**
 * Returns the cluster URL segment to use in API paths and the header
 * payload (if any). For shared clusters the segment is the URL-encoded
 * name; for private clusters it's the reserved sentinel and a header value
 * carries the full connection details.
 */
export function clusterRoute(clusterName: string): {
  segment: string;
  headerValue: string | null;
} {
  const priv = getPrivateClusterByName(clusterName);
  if (priv) {
    return {
      segment: PRIVATE_CLUSTER_SENTINEL,
      headerValue: encodePrivateClusterHeader(priv),
    };
  }
  return { segment: encodeURIComponent(clusterName), headerValue: null };
}

export function clusterPath(clusterName: string, subpath: string): string {
  const { segment } = clusterRoute(clusterName);
  const suffix = subpath.startsWith("/") ? subpath : "/" + subpath;
  return `/api/v1/clusters/${segment}${suffix}`;
}

/**
 * fetchAPI wraps window.fetch with the private-cluster header injection.
 * Callers pass the display cluster name; the helper rewrites URL/headers
 * transparently. Pass clusterName=null for non-cluster-scoped endpoints.
 */
export async function fetchAPI(
  clusterName: string | null,
  input: string,
  init: RequestInit = {},
): Promise<Response> {
  const headers = new Headers(init.headers ?? {});
  if (clusterName) {
    const { headerValue } = clusterRoute(clusterName);
    if (headerValue) headers.set("X-Kafkito-Cluster", headerValue);
  }
  return apiFetch(input, { ...init, headers });
}
