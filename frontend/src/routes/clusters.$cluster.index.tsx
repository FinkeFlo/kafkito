import { createFileRoute } from "@tanstack/react-router";
import { useCluster } from "@/lib/use-cluster";
import { PageHeader } from "@/components/page-header";

export const Route = createFileRoute("/clusters/$cluster/")({
  component: ClusterDashboard,
});

function ClusterDashboard() {
  const { cluster } = useCluster();
  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={<span className="font-mono normal-case tracking-normal">Cluster</span>}
        title={cluster ?? "Loading…"}
        subtitle="Cluster dashboard"
      />
      {/* TODO(backend): aggregate cluster KPIs (partitions, lag, throughput) */}
    </div>
  );
}
