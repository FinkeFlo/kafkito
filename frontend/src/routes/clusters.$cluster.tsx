import { createFileRoute, Outlet } from "@tanstack/react-router";
import { useEffect } from "react";
import { PrivateClusterPasswordGate } from "@/features/clusters/PrivateClusterPasswordGate";
import { setLastCluster } from "@/lib/last-cluster";

export const Route = createFileRoute("/clusters/$cluster")({
  component: ClusterLayout,
});

function ClusterLayout() {
  const { cluster } = Route.useParams();
  const decoded = decodeURIComponent(cluster);

  // The sidebar (topics / groups / schemas / brokers / acls navigation)
  // is rendered at __root level by the existing Shell. This layout only
  // ensures the Outlet is present and lastCluster is tracked. A private
  // cluster whose passwords this tab lacks asks for them first.
  return (
    <PrivateClusterPasswordGate cluster={decoded}>
      <TrackLastCluster cluster={decoded} />
      <Outlet />
    </PrivateClusterPasswordGate>
  );
}

// Persists the active cluster so `/` can auto-redirect on next visit. Inside
// the password gate, so leaving the gate with Cancel does not lead back to it.
function TrackLastCluster({ cluster }: { cluster: string }) {
  useEffect(() => {
    setLastCluster(cluster);
  }, [cluster]);
  return null;
}
