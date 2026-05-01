import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchTopicDetail, type PartitionInfo } from "@/lib/api";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/")({
  component: OverviewTab,
});

function OverviewTab() {
  const { cluster, topic } = Route.useParams();

  const detailQuery = useQuery({
    queryKey: ["topic", cluster, topic],
    queryFn: () => fetchTopicDetail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: 5_000,
  });

  if (detailQuery.isLoading && cluster) {
    return <div className="text-sm text-[var(--color-text-muted)]">Loading…</div>;
  }

  if (detailQuery.error) {
    return (
      <div className="rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger-subtle)] p-3 text-sm text-[var(--color-danger)]">
        Failed to load topic: {(detailQuery.error as Error).message}
      </div>
    );
  }

  if (!detailQuery.data) return null;

  return (
    <div className="space-y-6">
      <SummaryCards detail={detailQuery.data} />
      <PartitionsTable partitions={detailQuery.data.partitions} />
    </div>
  );
}

function SummaryCards({
  detail,
}: {
  detail: { partitions: PartitionInfo[]; replication_factor: number; messages: number; is_internal: boolean };
}) {
  const items = [
    { label: "Partitions", value: detail.partitions.length.toString() },
    { label: "Replication", value: detail.replication_factor.toString() },
    { label: "Messages", value: detail.messages.toLocaleString() },
    { label: "Kind", value: detail.is_internal ? "internal" : "user" },
  ];
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {items.map((i) => (
        <div
          key={i.label}
          className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-3"
        >
          <div className="text-xs uppercase tracking-wider text-[var(--color-text-muted)]">
            {i.label}
          </div>
          <div className="mt-1 font-mono text-lg tabular-nums">{i.value}</div>
        </div>
      ))}
    </div>
  );
}

function PartitionsTable({ partitions }: { partitions: PartitionInfo[] }) {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-sm">
      <div className="border-b border-[var(--color-border)] p-3 text-sm font-semibold">
        Partitions
      </div>
      <table className="w-full text-sm">
        <thead className="border-b border-[var(--color-border)] bg-[var(--color-surface-subtle)] text-left text-xs uppercase tracking-wider text-[var(--color-text-muted)]">
          <tr>
            <th className="px-4 py-2 font-semibold">#</th>
            <th className="px-4 py-2 font-semibold">Leader</th>
            <th className="px-4 py-2 font-semibold">Replicas</th>
            <th className="px-4 py-2 font-semibold">ISR</th>
            <th className="px-4 py-2 font-semibold">Start</th>
            <th className="px-4 py-2 font-semibold">End</th>
            <th className="px-4 py-2 font-semibold">Messages</th>
          </tr>
        </thead>
        <tbody>
          {partitions.map((p) => (
            <tr key={p.partition} className="border-b border-[var(--color-border)] last:border-0">
              <td className="px-4 py-2 font-mono tabular-nums">{p.partition}</td>
              <td className="px-4 py-2 tabular-nums">{p.leader}</td>
              <td className="px-4 py-2 font-mono text-xs text-[var(--color-text-muted)]">
                {p.replicas.join(", ")}
              </td>
              <td className="px-4 py-2 font-mono text-xs text-[var(--color-text-muted)]">
                {p.isr.join(", ")}
              </td>
              <td className="px-4 py-2 tabular-nums">{p.start_offset}</td>
              <td className="px-4 py-2 tabular-nums">{p.end_offset}</td>
              <td className="px-4 py-2 tabular-nums">{p.messages.toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
