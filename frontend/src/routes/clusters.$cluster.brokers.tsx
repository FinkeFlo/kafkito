import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Server } from "lucide-react";
import { useCluster } from "@/lib/use-cluster";
import { fetchBrokers, type BrokerInfo } from "@/lib/api";
import { KpiCard } from "@/components/KpiCard";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import {
  DataTable,
  DataTableHead,
  DataTableRow,
  DataTableTh,
} from "@/components/DataTable";
import { Tag } from "@/components/Tag";
import { StatusDot } from "@/components/StatusDot";

export const Route = createFileRoute("/clusters/$cluster/brokers")({
  component: BrokersPage,
});

function BrokersPage() {
  const { cluster, clusters } = useCluster();
  const active = clusters?.find((c) => c.name === cluster) ?? null;
  const canDescribe = active?.capabilities?.describe_cluster ?? false;

  const { data, isLoading, error } = useQuery({
    queryKey: ["brokers", cluster],
    queryFn: () => fetchBrokers(cluster as string),
    enabled: Boolean(cluster && canDescribe),
  });

  return (
    <div className="space-y-5 px-6 py-6">
      <div>
        <div className="flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted">
          <span>{cluster ?? "—"}</span>
          <span>›</span>
          <span>Brokers</span>
        </div>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Brokers</h1>
        <p className="mt-1 text-sm text-muted">
          Per-broker metadata for the active cluster.
        </p>
      </div>

      {!cluster && (
        <EmptyState
          icon={Server}
          title="No cluster selected"
          description="Pick a cluster from the header to inspect its brokers."
        />
      )}

      {cluster && !canDescribe && (
        <EmptyState
          icon={Server}
          title="DESCRIBE on CLUSTER:* missing"
          description={
            <span className="block">
              Kafkito cannot list brokers for this cluster. The connection user
              is missing <span className="font-mono">DESCRIBE</span> on{" "}
              <span className="font-mono">CLUSTER:*</span>, or the broker
              rejected the metadata probe.
            </span>
          }
        />
      )}

      {cluster && canDescribe && (
        <>
          <BrokerKpis brokers={data} />
          {error ? (
            <ErrorState
              title="Failed to load brokers"
              detail={error instanceof Error ? error.message : String(error)}
            />
          ) : isLoading ? (
            <EmptyState icon={Server} title="Loading brokers…" />
          ) : !data || data.length === 0 ? (
            <EmptyState icon={Server} title="No brokers reported" />
          ) : (
            <BrokerTable brokers={data} />
          )}
        </>
      )}
    </div>
  );
}

function BrokerKpis({ brokers }: { brokers: BrokerInfo[] | undefined }) {
  const total = brokers?.length;
  const racks = brokers
    ? new Set(brokers.map((b) => b.rack).filter((r): r is string => Boolean(r))).size
    : undefined;
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <KpiCard label="Brokers" value={total ?? "—"} />
      <KpiCard label="Racks" value={racks ?? "—"} />
      {/* TODO(backend): under-replicated/offline partitions need a per-cluster aggregate. */}
      <KpiCard label="Under-replicated" value="—" />
      {/* TODO(backend): JMX scraper for CPU/disk/heap; surface as KPIs. */}
      <KpiCard label="Avg CPU" value="—" unit="%" />
    </div>
  );
}

function BrokerTable({ brokers }: { brokers: BrokerInfo[] }) {
  return (
    <DataTable>
      <DataTableHead>
        <tr>
          <DataTableTh align="right">ID</DataTableTh>
          <DataTableTh>Host</DataTableTh>
          <DataTableTh align="right">Port</DataTableTh>
          <DataTableTh>Rack</DataTableTh>
          <DataTableTh>Role</DataTableTh>
        </tr>
      </DataTableHead>
      <tbody>
        {brokers.map((b) => (
          <DataTableRow key={b.node_id}>
            <td className="px-3 py-2 text-right font-mono tabular-nums">{b.node_id}</td>
            <td className="px-3 py-2 font-mono text-text">
              <span className="inline-flex items-center gap-2">
                <StatusDot reachable />
                {b.host}
              </span>
            </td>
            <td className="px-3 py-2 text-right font-mono tabular-nums text-muted">{b.port}</td>
            <td className="px-3 py-2 font-mono text-muted">{b.rack || "—"}</td>
            <td className="px-3 py-2">
              {b.is_controller ? (
                <Tag variant="info">controller</Tag>
              ) : (
                <Tag>broker</Tag>
              )}
            </td>
          </DataTableRow>
        ))}
      </tbody>
    </DataTable>
  );
}
