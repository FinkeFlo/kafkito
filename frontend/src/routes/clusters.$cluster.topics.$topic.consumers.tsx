import { useState } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Users } from "lucide-react";
import { fetchTopicConsumers, type TopicConsumer } from "@/lib/api";
import { Section } from "@/components/section";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import { Badge, type BadgeVariant } from "@/components/badge";
import { Button } from "@/components/button";
import { LagBadge } from "@/components/lag-badge";
import { CreateGroupModal } from "@/components/create-group-modal";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";
import { useFormatters } from "@/lib/use-formatters";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/consumers")({
  component: ConsumersTab,
});

function ConsumersTab() {
  const { cluster, topic } = Route.useParams();
  return <ConsumersPanel cluster={cluster} topic={topic} />;
}

function ConsumersPanel({ cluster, topic }: { cluster: string; topic: string }) {
  const fmt = useFormatters();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useQuery({
    queryKey: ["topic-consumers", cluster, topic],
    queryFn: () => fetchTopicConsumers(cluster, topic),
    enabled: Boolean(cluster && topic),
    staleTime: 10_000,
  });

  const stateVariant = (state: string): BadgeVariant => {
    switch (state) {
      case "Stable":
        return "success";
      case "PreparingRebalance":
      case "CompletingRebalance":
        return "warning";
      case "Empty":
        return "neutral";
      case "Dead":
        return "danger";
      default:
        return "neutral";
    }
  };

  const columns: DataTableColumn<TopicConsumer>[] = [
    {
      id: "group_id",
      header: "Group",
      sortValue: (r) => r.group_id,
      cell: (r) => (
        <Link
          to="/clusters/$cluster/groups"
          params={{ cluster }}
          search={{ group: r.group_id }}
          className="font-mono text-[13px] tabular-nums text-accent hover:underline"
        >
          {r.group_id}
        </Link>
      ),
    },
    {
      id: "state",
      header: "State",
      sortValue: (r) => r.state,
      cell: (r) => <Badge variant={stateVariant(r.state)}>{r.state || "—"}</Badge>,
    },
    {
      id: "members",
      header: "Members",
      sortValue: (r) => r.members,
      align: "right",
      className: "tabular-nums",
      cell: (r) => fmt.number(r.members),
    },
    {
      id: "partitions",
      header: "Partitions",
      sortValue: (r) => r.partitions_assigned.length,
      align: "right",
      className: "tabular-nums",
      cell: (r) => fmt.number(r.partitions_assigned.length),
    },
    {
      id: "lag",
      header: "Lag",
      sortValue: (r) => (r.lag_known ? r.lag : -1),
      align: "right",
      cell: (r) =>
        r.lag_known ? <LagBadge value={r.lag} /> : <span className="text-subtle-text">—</span>,
    },
  ];

  let errorBanner: string | null = null;
  if (query.isError) {
    const msg = (query.error as Error).message ?? "";
    if (msg.includes("topic_consumers_timeout")) {
      errorBanner = "Listing consumers for this topic took too long. Try again in a moment.";
    } else {
      errorBanner = `Failed to load consumers: ${msg}`;
    }
  }

  return (
    <Section
      title="Consumers"
      description="Consumer groups currently reading from this topic."
      actions={
        <Button variant="secondary" onClick={() => setCreateOpen(true)}>
          Create consumer group
        </Button>
      }
    >
      {errorBanner ? (
        <Notice intent="danger">{errorBanner}</Notice>
      ) : (
        <DataTable<TopicConsumer>
          columns={columns}
          rows={query.data}
          rowKey={(r) => r.group_id}
          isLoading={query.isLoading}
          skeletonRows={3}
          emptyState={
            <EmptyState
              icon={<Users className="h-6 w-6" />}
              title="No consumers"
              description="No consumer group is currently reading from this topic."
            />
          }
        />
      )}
      {createOpen && (
        <CreateGroupModal cluster={cluster} topic={topic} onClose={() => setCreateOpen(false)} />
      )}
    </Section>
  );
}
