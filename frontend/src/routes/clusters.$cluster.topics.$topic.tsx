import { createFileRoute, Link, Outlet, useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import {
  fetchClusters,
  fetchTopicConsumers,
  fetchTopicDetail,
  deleteTopic,
  deleteRecords,
  can,
  type Capabilities,
  type PartitionInfo,
  type TopicDetail,
} from "@/lib/api";
import { useAuth } from "@/auth/hooks";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Tag } from "@/components/Tag";
import { KpiCard } from "@/components/KpiCard";
import { Button } from "@/components/button";
import { Modal } from "@/components/Modal";
import { Notice } from "@/components/Notice";
import { Input } from "@/components/Input";
import { formatBytes, formatCount } from "@/lib/format";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic")({
  component: TopicDetailLayout,
});

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "messages", label: "Messages" },
  { id: "produce", label: "Produce" },
  { id: "configs", label: "Configs" },
  { id: "consumers", label: "Consumers" },
  { id: "schema", label: "Schema" },
] as const;

type TabPath =
  | "/clusters/$cluster/topics/$topic"
  | "/clusters/$cluster/topics/$topic/messages"
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

  const clustersQuery = useQuery({
    queryKey: ["clusters"],
    queryFn: fetchClusters,
  });
  const caps = useMemo(
    () => clustersQuery.data?.find((c) => c.name === cluster)?.capabilities,
    [clustersQuery.data, cluster],
  );

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

  return (
    <div className="space-y-5 px-6 py-6">
      <div>
        <Breadcrumbs cluster={cluster} topic={topic} />
        <div className="mt-1 flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="font-mono text-2xl font-semibold tracking-tight">{topic}</h1>
            {detailQuery.data?.is_internal && <Tag>INTERNAL</Tag>}
            {schemaType && <Tag variant="info">{schemaType.toUpperCase()}</Tag>}
          </div>
          {cluster && detailQuery.data && (
            <TopicActions
              cluster={cluster}
              topic={topic}
              partitions={detailQuery.data.partitions}
              caps={caps}
            />
          )}
        </div>
        <p className="mt-1 text-sm text-muted">
          {detailQuery.data
            ? `${detailQuery.data.partitions.length} partitions · RF ${detailQuery.data.replication_factor} · ${retentionLabel(retention)}`
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
            // Sub-tab routes (messages/configs/consumers/schema/produce) are
            // created in Task 6 of the URL Hierarchy Redesign — until then the
            // generated route table doesn't list them, so we cast to satisfy
            // TanStack's strict typed-router. Remove `as never` once Task 6
            // lands.
            to={tabPath(tab.id) as never}
            params={{ cluster, topic } as never}
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

function retentionLabel(ms: string | undefined): string {
  if (!ms) return "retention —";
  const n = Number(ms);
  if (!Number.isFinite(n)) return `retention ${ms}`;
  if (n <= 0) return "retention ∞";
  const hours = n / 3_600_000;
  if (hours < 24) return `${hours.toFixed(1)}h retention`;
  const days = hours / 24;
  return `${days.toFixed(1)}d retention`;
}

function KpiStrip({
  detail,
  consumers,
}: {
  detail: TopicDetail | undefined;
  consumers: Array<{ lag: number; lag_known: boolean }> | undefined;
}) {
  const msgs = detail ? formatCount(detail.messages) : "—";
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
        value={totalLag === undefined ? "—" : lagKnown ? formatCount(totalLag) : "—"}
        delta={totalLag === 0 ? "healthy" : undefined}
        deltaIntent={totalLag === 0 ? "good" : "neutral"}
      />
      <KpiCard
        label="Avg msg size"
        value={
          detail && detail.size_bytes != null && detail.messages > 0
            ? formatBytes(Math.round(detail.size_bytes / Number(detail.messages)))
            : "—"
        }
        unit="per message"
      />
      <KpiCard label="Consumers" value={consumerCount} />
    </div>
  );
}

function TopicActions({
  cluster,
  topic,
  partitions,
  caps,
}: {
  cluster: string;
  topic: string;
  partitions: PartitionInfo[];
  caps?: Capabilities;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [truncOpen, setTruncOpen] = useState(false);
  const [delOpen, setDelOpen] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const delMut = useMutation({
    mutationFn: () => deleteTopic(cluster, topic),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["topics", cluster] });
      navigate({ to: "/clusters/$cluster/topics", params: { cluster } });
    },
    onError: (e: Error) => setErr(e.message),
  });

  const { me } = useAuth();
  const rbacAllowsDelete = can(me, cluster, "topic", "delete", topic);
  const canDelete = caps?.delete_topic !== false && rbacAllowsDelete;
  const canTruncate = caps?.delete_topic !== false && rbacAllowsDelete;
  const deleteReason = !rbacAllowsDelete
    ? "forbidden by RBAC policy"
    : (caps?.errors?.delete_topic ?? "DELETE on TOPIC required");

  // RBAC / capability reason is load-bearing — wire it via aria-describedby
  // and an inline `<Notice>` instead of hover-only `title=`.
  const truncReasonId = "topic-truncate-disabled-reason";
  const delReasonId = "topic-delete-disabled-reason";
  return (
    <div className="flex flex-col items-end gap-2">
      <div className="flex gap-2">
        <Button
          variant="secondary"
          size="sm"
          onClick={() => setTruncOpen(true)}
          disabled={!canTruncate}
          aria-describedby={!canTruncate ? truncReasonId : undefined}
        >
          Delete records…
        </Button>
        <Button
          variant="danger"
          size="sm"
          onClick={() => setDelOpen(true)}
          disabled={delMut.isPending || !canDelete}
          aria-describedby={!canDelete ? delReasonId : undefined}
        >
          {delMut.isPending ? "Deleting…" : "Delete topic"}
        </Button>
      </div>
      {!canTruncate ? (
        <span id={truncReasonId} className="sr-only">
          {deleteReason}
        </span>
      ) : null}
      {!canDelete ? (
        <span id={delReasonId} className="sr-only">
          {deleteReason}
        </span>
      ) : null}
      <ConfirmDialog
        open={delOpen}
        onOpenChange={setDelOpen}
        title={`Delete topic "${topic}"?`}
        description={`This will permanently remove the topic on cluster "${cluster}". This cannot be undone.`}
        confirmPhrase={topic}
        confirmLabel="Delete topic"
        variant="danger"
        onConfirm={() => {
          setErr(null);
          delMut.mutate();
        }}
      />
      {err && (
        <div className="max-w-xs">
          <Notice intent="danger">{err}</Notice>
        </div>
      )}
      {truncOpen && (
        <DeleteRecordsModal
          cluster={cluster}
          topic={topic}
          partitions={partitions}
          onClose={() => setTruncOpen(false)}
        />
      )}
    </div>
  );
}

function DeleteRecordsModal({
  cluster,
  topic,
  partitions,
  onClose,
}: {
  cluster: string;
  topic: string;
  partitions: PartitionInfo[];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [sel, setSel] = useState<Record<number, boolean>>(() =>
    Object.fromEntries(partitions.map((p) => [p.partition, true])),
  );
  const [mode, setMode] = useState<"all" | "offset">("all");
  const [offsets, setOffsets] = useState<Record<number, string>>(() =>
    Object.fromEntries(partitions.map((p) => [p.partition, String(p.end_offset)])),
  );
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<
    | { partition: number; low_watermark: number; error?: string }[]
    | null
  >(null);

  const mut = useMutation({
    mutationFn: async () => {
      const body: Record<number, number> = {};
      for (const p of partitions) {
        if (!sel[p.partition]) continue;
        body[p.partition] = mode === "all" ? -1 : Number(offsets[p.partition] ?? 0);
      }
      return deleteRecords(cluster, topic, body);
    },
    onSuccess: (r) => {
      setResult(r.results);
      qc.invalidateQueries({ queryKey: ["topic", cluster, topic] });
      qc.invalidateQueries({ queryKey: ["messages", cluster, topic] });
    },
    onError: (e: Error) => setErr(e.message),
  });

  const anySelected = Object.values(sel).some(Boolean);

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={
        <span className="flex flex-col">
          <span className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            Delete records from
          </span>
          <span className="font-mono text-[14px] font-semibold">{topic}</span>
        </span>
      }
      actions={
        <>
          <span className="mr-auto text-xs text-muted">
            {formatBytes(0)} to be freed · estimate
          </span>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
          <Button
            variant="danger"
            size="sm"
            disabled={!anySelected || mut.isPending}
            onClick={() => {
              setErr(null);
              setResult(null);
              mut.mutate();
            }}
          >
            {mut.isPending ? "Running…" : "Delete records"}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-sm">
        <Notice intent="warning">
          This truncates the log per partition. Consumers reading older offsets
          will skip ahead. Cannot be undone.
        </Notice>
        <div className="flex gap-4 text-xs">
          <label className="flex items-center gap-1">
            <input
              type="radio"
              checked={mode === "all"}
              onChange={() => setMode("all")}
            />
            Delete all (truncate to end)
          </label>
          <label className="flex items-center gap-1">
            <input
              type="radio"
              checked={mode === "offset"}
              onChange={() => setMode("offset")}
            />
            Keep records at/after offset
          </label>
        </div>
        <table className="w-full text-xs">
          <thead className="text-left text-[11px] uppercase tracking-wider text-muted">
            <tr>
              <th className="py-1"></th>
              <th className="py-1">Partition</th>
              <th className="py-1 text-right">Start</th>
              <th className="py-1 text-right">End</th>
              {mode === "offset" && <th className="py-1 text-right">Keep from</th>}
            </tr>
          </thead>
          <tbody>
            {partitions.map((p) => (
              <tr key={p.partition} className="border-t border-border">
                <td className="py-1">
                  <input
                    type="checkbox"
                    checked={!!sel[p.partition]}
                    onChange={(e) =>
                      setSel((s) => ({ ...s, [p.partition]: e.target.checked }))
                    }
                  />
                </td>
                <td className="py-1 font-mono text-[13px] tabular-nums">{p.partition}</td>
                <td className="py-1 text-right font-mono text-[13px] tabular-nums text-muted">
                  {p.start_offset}
                </td>
                <td className="py-1 text-right font-mono text-[13px] tabular-nums text-muted">
                  {p.end_offset}
                </td>
                {mode === "offset" && (
                  <td className="py-1 text-right">
                    <Input
                      value={offsets[p.partition] ?? ""}
                      onChange={(e) =>
                        setOffsets((o) => ({
                          ...o,
                          [p.partition]: e.target.value,
                        }))
                      }
                      className="h-7 w-24 text-right font-mono text-xs"
                    />
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
        {result && (
          <div className="rounded-md border border-border bg-subtle p-2 text-xs">
            <div className="mb-1 font-semibold">Result</div>
            <ul className="space-y-0.5 font-mono">
              {result.map((r) => (
                <li key={r.partition}>
                  p{r.partition}:{" "}
                  {r.error ? (
                    <span className="text-danger">{r.error}</span>
                  ) : (
                    <span className="text-success">
                      low-watermark = {r.low_watermark}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
        {err && <Notice intent="danger">{err}</Notice>}
      </div>
    </Modal>
  );
}
