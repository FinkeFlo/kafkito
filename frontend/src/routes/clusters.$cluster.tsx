import { createFileRoute, Outlet } from "@tanstack/react-router";
import { useEffect } from "react";
import { setLastCluster } from "@/lib/last-cluster";

export const Route = createFileRoute("/clusters/$cluster")({
  component: ClusterLayout,
});

function ClusterLayout() {
  const { cluster } = Route.useParams();
  const decoded = decodeURIComponent(cluster);

  // Persist the active cluster so `/` can auto-redirect on next visit.
  useEffect(() => {
    setLastCluster(decoded);
  }, [decoded]);

  // The sidebar (topics / groups / schemas / brokers / acls navigation)
  // is rendered at __root level by the existing Shell. This layout only
  // ensures the Outlet is present and lastCluster is tracked.
  return <Outlet />;
}
