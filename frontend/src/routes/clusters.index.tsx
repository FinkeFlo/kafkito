import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Network } from "lucide-react";
import { clsx } from "clsx";
import { useEffect, useMemo, useState } from "react";
import {
  fetchClusters,
  type ClusterInfo,
} from "@/lib/api";
import {
  listPrivateClusters,
  subscribePrivateClusters,
  type PrivateCluster,
} from "@/lib/private-clusters";
import { StatusDot } from "@/components/StatusDot";
import { Tag } from "@/components/Tag";
import { KpiCard } from "@/components/KpiCard";
import { DataTable, DataTableHead, DataTableRow, DataTableTh } from "@/components/DataTable";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/button";
import { formatCount, formatNumber, formatRate, formatRelative } from "@/lib/format";

type ClusterRowInfo = ClusterInfo & { is_private?: boolean };

function privateClusterToRow(c: PrivateCluster): ClusterRowInfo {
  return {
    name: c.name,
    // Private clusters are not probed automatically — assume reachable so
    // the row doesn't carry a misleading red UNREACHABLE pill. Real
    // reachability surfaces when the user navigates into Topics/Brokers.
    reachable: true,
    auth_type: c.auth.type,
    tls: !!c.tls?.enabled,
    schema_registry: !!c.schema_registry,
    is_private: true,
  };
}

export const Route = createFileRoute("/clusters/")({
  validateSearch: (s: Record<string, unknown>) => ({
    cluster: typeof s.cluster === "string" ? s.cluster : undefined,
  }),
  component: HomePage,
});

function environmentOf(name: string): string {
  // Heuristic: split on the first hyphen (e.g. "prod-eu-west" → "prod").
  const idx = name.indexOf("-");
  return idx > 0 ? name.slice(0, idx) : name;
}

function isLimited(c: ClusterInfo): boolean {
  return !!c.capabilities?.errors && Object.keys(c.capabilities.errors).length > 0;
}

function HomePage() {
  const clustersQuery = useQuery({
    queryKey: ["clusters"],
    queryFn: fetchClusters,
    staleTime: 10_000,
    refetchInterval: 30_000,
  });

  const [privateClusters, setPrivateClusters] = useState<PrivateCluster[]>(() =>
    listPrivateClusters(),
  );
  useEffect(() => {
    const unsub = subscribePrivateClusters(() =>
      setPrivateClusters(listPrivateClusters()),
    );
    return unsub;
  }, []);

  const merged: ClusterRowInfo[] | undefined = useMemo(() => {
    if (!clustersQuery.data) return undefined;
    const serverNames = new Set(clustersQuery.data.map((c) => c.name));
    const privateRows = privateClusters
      .filter((p) => !serverNames.has(p.name))
      .map(privateClusterToRow);
    return [...clustersQuery.data, ...privateRows];
  }, [clustersQuery.data, privateClusters]);

  return (
    <div className="space-y-5 p-6">
      <Header
        clusters={merged}
        lastFetched={clustersQuery.dataUpdatedAt}
        loading={clustersQuery.isLoading}
      />

      {clustersQuery.isLoading && <KpiSkeleton />}
      {!clustersQuery.isLoading && merged && <Kpis clusters={merged} />}

      {clustersQuery.isError && (
        <ErrorState
          title="Failed to load clusters"
          detail={String(clustersQuery.error)}
          onRetry={() => clustersQuery.refetch()}
        />
      )}

      {!clustersQuery.isLoading && merged && merged.length === 0 && (
        <EmptyState
          icon={Network}
          title="Welcome to kafkito"
          description="No clusters are connected yet. Add your first bootstrap server and kafkito will enumerate topics, groups, and schemas."
          action={
            <Link
              to="/settings/clusters"
              search={{ cluster: undefined }}
            >
              <Button variant="primary">+ Connect cluster</Button>
            </Link>
          }
        />
      )}

      {!clustersQuery.isLoading && merged && merged.length > 0 && (
        <ClustersTable clusters={merged} />
      )}

      {clustersQuery.isLoading && <TableSkeleton />}
    </div>
  );
}

function Header({
  clusters,
  lastFetched,
  loading,
}: {
  clusters: ClusterRowInfo[] | undefined;
  lastFetched: number;
  loading: boolean;
}) {
  const count = clusters?.length ?? 0;
  const envs = new Set((clusters ?? []).map((c) => environmentOf(c.name)));
  const envCount = envs.size || 0;
  const relative = lastFetched ? formatRelative(lastFetched) : "—";

  // Disabled "Export" carries load-bearing info — surface it via
  // `aria-describedby` instead of hover-only `title`.
  const exportReasonId = "home-export-disabled-reason";
  return (
    <PageHeader
      eyebrow={
        <>
          <span className="font-mono normal-case tracking-normal">All clusters</span>{" "}
          <span aria-hidden>›</span> Fleet overview
        </>
      }
      title={
        loading
          ? "Loading clusters…"
          : count === 0
            ? "Welcome to kafkito"
            : `${count} Kafka ${count === 1 ? "cluster" : "clusters"} across ${envCount} ${envCount === 1 ? "environment" : "environments"}`
      }
      subtitle={`Last health check · ${relative}`}
      actions={
        <>
          <Button
            variant="secondary"
            size="sm"
            disabled
            aria-describedby={exportReasonId}
          >
            Export
          </Button>
          <span id={exportReasonId} className="sr-only">
            Export not wired up yet.
          </span>
          <Link to="/settings/clusters" search={{ cluster: undefined }}>
            <Button variant="primary" size="sm">
              + Connect cluster
            </Button>
          </Link>
        </>
      }
    />
  );
}

function Kpis({ clusters }: { clusters: ClusterRowInfo[] }) {
  const unreachable = clusters.filter((c) => !c.reachable);
  const totals = aggregateTotals(clusters);
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <KpiCard
        label="Total throughput"
        value={totals.rate !== null ? formatRate(totals.rate) : "—"}
        unit="msg/s"
      />
      <KpiCard
        label="Total lag"
        value={totals.lag !== null ? formatCount(totals.lag) : "—"}
        unit="messages"
      />
      <KpiCard
        label="Topics"
        value={totals.topics !== null ? formatNumber(totals.topics) : "—"}
        unit="across clusters"
      />
      {/* TODO(backend): /clusters/health/incidents?window=24h needed for a real Incidents-24h metric */}
      <KpiCard
        label="Unreachable now"
        value={unreachable.length}
        delta={unreachable.length > 0 ? unreachable.map((c) => c.name).join(", ") : "none"}
        deltaIntent={unreachable.length > 0 ? "bad" : "good"}
      />
    </div>
  );
}

// aggregateTotals sums optional per-cluster metric fields; returns null for
// each metric when no cluster has reported it yet, so the UI shows "—"
// instead of a misleading "0 across clusters".
function aggregateTotals(clusters: ClusterRowInfo[]): {
  rate: number | null;
  lag: number | null;
  topics: number | null;
} {
  let rate = 0;
  let lag = 0;
  let topics = 0;
  let haveRate = false;
  let haveLag = false;
  let haveTopics = false;
  for (const c of clusters) {
    if (typeof c.total_rate_per_sec === "number") {
      rate += c.total_rate_per_sec;
      haveRate = true;
    }
    if (typeof c.total_lag === "number") {
      lag += c.total_lag;
      haveLag = true;
    }
    if (typeof c.topics === "number") {
      topics += c.topics;
      haveTopics = true;
    }
  }
  return {
    rate: haveRate ? rate : null,
    lag: haveLag ? lag : null,
    topics: haveTopics ? topics : null,
  };
}

function ClustersTable({ clusters }: { clusters: ClusterRowInfo[] }) {
  const sorted = [...clusters].sort((a, b) => a.name.localeCompare(b.name));
  const privateCount = clusters.filter((c) => c.is_private).length;
  const subtitleParts = [
    `${clusters.length} total`,
    `${clusters.filter((c) => c.reachable).length} reachable`,
  ];
  if (privateCount > 0) {
    subtitleParts.push(
      `${privateCount} private (browser-local)`,
    );
  }
  return (
    <DataTable
      title="Clusters"
      subtitle={subtitleParts.join(" · ")}
    >
      <DataTableHead>
        <tr>
          <DataTableTh>Cluster</DataTableTh>
          <DataTableTh>Security</DataTableTh>
          <DataTableTh align="right">Brokers</DataTableTh>
          <DataTableTh align="right">Topics</DataTableTh>
          <DataTableTh align="right">Groups</DataTableTh>
          <DataTableTh align="right">Throughput</DataTableTh>
          <DataTableTh align="right">Lag</DataTableTh>
        </tr>
      </DataTableHead>
      <tbody>
        {sorted.map((c) => (
          <ClusterRow key={c.name} cluster={c} />
        ))}
      </tbody>
    </DataTable>
  );
}

function ClusterRow({ cluster }: { cluster: ClusterRowInfo }) {
  const limited = isLimited(cluster);
  return (
    <DataTableRow>
      <td className="px-4 py-3">
        <Link
          to="/clusters/$cluster"
          params={{ cluster: cluster.name }}
          className="block -my-3 -mx-4 px-4 py-3 text-left"
        >
          <div className="flex items-center gap-2">
            <StatusDot reachable={cluster.reachable} />
            <span className="font-mono text-[13px] tabular-nums font-semibold text-text">
              {cluster.name}
            </span>
            {cluster.is_private && <Tag variant="info">PRIVATE</Tag>}
            {limited && <Tag variant="warn">LIMITED</Tag>}
            {!cluster.reachable && <Tag variant="danger">UNREACHABLE</Tag>}
          </div>
          {cluster.error && (
            <div className="mt-1 font-mono text-[11px] text-danger">{cluster.error}</div>
          )}
        </Link>
      </td>
      <td className="px-4 py-3">
        <div className="flex flex-wrap gap-1">
          {cluster.tls && <Tag>TLS</Tag>}
          {cluster.auth_type && cluster.auth_type !== "none" && (
            <Tag>{cluster.auth_type}</Tag>
          )}
          {cluster.schema_registry && <Tag variant="info">SR</Tag>}
        </div>
      </td>
      {/* Optional aggregates — collector fills them asynchronously; shown
          as "—" until the first refresh completes.
          TODO(backend): /clusters brokers count
          TODO(backend): /clusters topics count
          TODO(backend): /clusters groups count
          TODO(backend): /clusters total_rate_per_sec aggregate
          TODO(backend): /clusters total_lag aggregate */}
      <MetricTd value={cluster.brokers} format={formatNumber} />
      <MetricTd value={cluster.topics} format={formatNumber} />
      <MetricTd value={cluster.groups} format={formatNumber} />
      <MetricTd value={cluster.total_rate_per_sec} format={formatRate} />
      <MetricTd value={cluster.total_lag} format={formatCount} />
    </DataTableRow>
  );
}

function MetricTd({
  value,
  format,
}: {
  value: number | null | undefined;
  format: (n: number) => string;
}) {
  const known = value !== null && value !== undefined;
  return (
    <td
      className={clsx(
        "px-4 py-3 text-right font-mono text-[13px] tabular-nums",
        known ? "text-text" : "text-subtle-text",
      )}
    >
      {known ? format(value) : "—"}
    </td>
  );
}

function KpiSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {[0, 1, 2, 3].map((i) => (
        <div key={i} className="h-24 animate-pulse rounded-xl border border-border bg-panel" />
      ))}
    </div>
  );
}

function TableSkeleton() {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-panel">
      <div className="border-b border-border px-4 py-3">
        <div className="h-4 w-24 animate-pulse rounded bg-subtle" />
      </div>
      {[0, 1, 2, 3, 4].map((i) => (
        <div key={i} className="flex items-center gap-4 border-t border-border px-4 py-3">
          <div className="h-3 w-40 animate-pulse rounded bg-subtle" />
          <div className="h-3 w-16 animate-pulse rounded bg-subtle" />
          <div className="ml-auto h-3 w-20 animate-pulse rounded bg-subtle" />
        </div>
      ))}
    </div>
  );
}
