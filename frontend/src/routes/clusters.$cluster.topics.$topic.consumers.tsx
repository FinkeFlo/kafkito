import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Users } from "lucide-react";
import {
  fetchTopicConsumers,
  type TopicConsumer,
} from "@/lib/api";
import { Section } from "@/components/section";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import { Badge, type BadgeVariant } from "@/components/badge";
import { LagBadge } from "@/components/lag-badge";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";
import { formatNumber } from "@/lib/format";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/consumers")({
  component: ConsumersTab,
});

function ConsumersTab() {
  const { cluster, topic } = Route.useParams();
  return <ConsumersPanel cluster={cluster} topic={topic} />;
}

function ConsumersPanel({ cluster, topic }: { cluster: string; topic: string }) {
  const { t } = useTranslation("topics");
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
      header: t("consumers.columns.groupId"),
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
      header: t("consumers.columns.state"),
      sortValue: (r) => r.state,
      cell: (r) => <Badge variant={stateVariant(r.state)}>{r.state || "—"}</Badge>,
    },
    {
      id: "members",
      header: t("consumers.columns.members"),
      sortValue: (r) => r.members,
      align: "right",
      className: "tabular-nums",
      cell: (r) => formatNumber(r.members),
    },
    {
      id: "partitions",
      header: t("consumers.columns.partitions"),
      sortValue: (r) => r.partitions_assigned.length,
      align: "right",
      className: "tabular-nums",
      cell: (r) => formatNumber(r.partitions_assigned.length),
    },
    {
      id: "lag",
      header: t("consumers.columns.lag"),
      sortValue: (r) => (r.lag_known ? r.lag : -1),
      align: "right",
      cell: (r) => (r.lag_known ? <LagBadge value={r.lag} /> : <span className="text-subtle-text">—</span>),
    },
  ];

  let errorBanner: string | null = null;
  if (query.isError) {
    const msg = (query.error as Error).message ?? "";
    if (msg.includes("topic_consumers_timeout")) {
      errorBanner = t("consumers.errorTimeout");
    } else {
      errorBanner = t("consumers.errorGeneric", { detail: msg });
    }
  }

  return (
    <Section title={t("consumers.title")} description={t("consumers.description")}>
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
              title={t("consumers.empty.title")}
              description={t("consumers.empty.description")}
            />
          }
        />
      )}
    </Section>
  );
}
