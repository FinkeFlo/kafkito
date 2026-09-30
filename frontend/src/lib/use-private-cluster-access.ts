import { useQuery } from "@tanstack/react-query";
import type { Me } from "@/auth/types";
import { authQueries } from "@/lib/queries/auth";

export interface PrivateClusterAccess {
  /** False only when GET /api/v1/me reports that the caller may not use private clusters. */
  allowed: boolean;
  /** The server's `private_clusters.mode`; undefined until /me has loaded. */
  mode?: Me["private_clusters"]["mode"];
}

/**
 * Whether the server lets the current user use private clusters. Until /me
 * has loaded, or when it fails, they count as allowed: the server checks
 * every private-cluster request itself, so this only decides what the UI
 * offers.
 */
export function usePrivateClusterAccess(): PrivateClusterAccess {
  // AuthProvider owns the /me request; this observer only reads its result,
  // so components using the hook add no request of their own.
  const status = useQuery({ ...authQueries.me(), enabled: false }).data?.private_clusters;
  return { allowed: status?.allowed !== false, mode: status?.mode };
}
