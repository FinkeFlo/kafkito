import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  fetchTopicConsumers,
  fetchTopicDetail,
  type TopicDetail,
} from "@/lib/api";
import { Tag } from "@/components/Tag";
import { KpiCard } from "@/components/KpiCard";
import { Notice } from "@/components/Notice";
import { useFormatters, type Formatters } from "@/lib/use-formatters";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic")({
  component: TopicDetailLayout,
});

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "messages", label: "Messages" },
  { id: "timeline", label: "Timeline" },
  { id: "produce", label: "Produce" },
  { id: "configs", label: "Configs" },
  { id: "consumers", label: "Consumers" },
  { id: "schema", label: "Schema" },
] as const;

type TabPath =
  | "/clusters/$cluster/topics/$topic"
  | "/clusters/$cluster/topics/$topic/messages"
  | "/clusters/$cluster/topics/$topic/timeline"
  | "/clusters/$cluster/topics/$topic/produce"
  | "/clusters/$cluster/topics/$topic/configs"
  | "/clusters/$cluster/topics/$topic/consumers"
  | "/clusters/$cluster/topics/$topic/schema";

function tabPath(id: string): TabPath {
  if (id === "overview") return "/clusters/$cluster/topics/$topic";
  return `/clusters/$cluster/topics/$topic/${id}` as TabPath;
}

function TopicDetailLayout() {
  const { cluster, topic } = Route.useParams();
  const fmt = useFormatters();

  const detailQuery = useQuery({
    queryKey: ["topic", cluster, topic],
    queryFn: () => fetchTopicDetail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: 5_000,
  });

  const consumersQuery = useQuery({
    queryKey: ["topic-consumers", cluster, topic],
    queryFn: () => fetchTopicConsumers(cluster, topic),
    enabled: !!cluster,
    staleTime: 10_000,
  });

  const schemaType = detailQuery.data?.configs?.find(
    (c) => c.name === "compression.type" || c.name === "value.schema.type",
  )?.value;
  const retention = detailQuery.data?.configs?.find((c) => c.name === "retention.ms")?.value;
  const configsError = detailQuery.data?.configs_error;

  return (
    <div className="space-y-5 px-6 py-6">
      <div>
        <Breadcrumbs cluster={cluster} topic={topic} />
        <div className="mt-1 flex flex-wrap items-center gap-3">
          <h1 className="font-mono text-2xl font-semibold tracking-tight">{topic}</h1>
          {detailQuery.data?.is_internal && <Tag>INTERNAL</Tag>}
          {schemaType && <Tag variant="info">{schemaType.toUpperCase()}</Tag>}
        </div>
        <p className="mt-1 text-sm text-muted">
          {detailQuery.data
            ? `${detailQuery.data.partitions.length} partitions · RF ${detailQuery.data.replication_factor} · ${retentionLabel(retention, configsError, fmt)}`
            : "Loading topic metadata…"}
        </p>
      </div>

      {!cluster && (
        <Notice intent="warning">
          Pick a cluster from the header to load topic detail.
        </Notice>
      )}

      <KpiStrip detail={detailQuery.data} consumers={consumersQuery.data} />

      <nav className="flex items-center gap-1 border-b border-border">
        {TABS.map((tab) => (
          <Link
            key={tab.id}
            to={tabPath(tab.id)}
            params={{ cluster, topic }}
            className="relative px-3 py-2 text-sm font-medium text-muted transition-colors hover:text-text"
            activeOptions={{ exact: tab.id === "overview" }}
            activeProps={{
              className:
                "relative px-3 py-2 text-sm font-semibold text-text after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-accent",
            }}
          >
            {tab.label}
          </Link>
        ))}
      </nav>

      <Outlet />
    </div>
  );
}

function Breadcrumbs({ cluster, topic }: { cluster: string; topic: string }) {
  return (
    <div className="flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted">
      <span>{cluster || "—"}</span>
      <span>›</span>
      <Link
        to="/clusters/$cluster/topics"
        params={{ cluster }}
        className="hover:text-text"
      >
        Topics
      </Link>
      <span>›</span>
      <span className="text-text">{topic}</span>
    </div>
  );
}

function retentionLabel(
  ms: string | undefined,
  configsError: string | undefined,
  fmt: Formatters,
): string {
  if (!ms) {
    if (configsError === "unauthorized") return "retention restricted";
    if (configsError) return "retention unavailable";
    return "retention —";
  }
  const n = Number(ms);
  if (!Number.isFinite(n)) return `retention ${ms}`;
  if (n <= 0) return "retention ∞";
  const hours = n / 3_600_000;
  if (hours < 24) return `${fmt.decimal(hours, 1)}h retention`;
  const days = hours / 24;
  return `${fmt.decimal(days, 1)}d retention`;
}

function KpiStrip({
  detail,
  consumers,
}: {
  detail: TopicDetail | undefined;
  consumers: Array<{ lag: number; lag_known: boolean }> | undefined;
}) {
  const fmt = useFormatters();
  const msgs = detail ? fmt.count(detail.messages) : "—";
  const totalLag = consumers
    ? consumers.reduce((s, c) => s + (c.lag_known ? c.lag : 0), 0)
    : undefined;
  const lagKnown = consumers ? consumers.every((c) => c.lag_known) : false;
  const consumerCount = consumers?.length ?? "—";

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <KpiCard label="Messages" value={msgs} />
      <KpiCard
        label="Lag (all groups)"
        value={totalLag === undefined ? "—" : lagKnown ? fmt.count(totalLag) : "—"}
        delta={totalLag === 0 ? "healthy" : undefined}
        deltaIntent={totalLag === 0 ? "good" : "neutral"}
      />
      <KpiCard
        label="Avg msg size"
        value={
          detail && detail.size_bytes != null && detail.messages > 0
            ? fmt.bytes(Math.round(detail.size_bytes / Number(detail.messages)))
            : "—"
        }
        unit="per message"
      />
      <KpiCard label="Consumers" value={consumerCount} />
    </div>
  );
}
