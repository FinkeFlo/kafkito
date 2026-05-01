import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Boxes, ChevronDown } from "lucide-react";
import { clsx } from "clsx";
import {
  fetchTopics,
  createTopic,
  can,
  type ClusterInfo,
  type TopicInfo,
} from "@/lib/api";
import { useAuth } from "@/auth/hooks";
import { useCluster } from "@/lib/use-cluster";
import { Tag } from "@/components/Tag";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { DataTable, DataTableHead, DataTableRow, DataTableTh } from "@/components/DataTable";
import { SearchInput } from "@/components/search-input";
import { Highlight } from "@/components/highlight";
import { PageHeader } from "@/components/page-header";
import { Toolbar } from "@/components/Toolbar";
import { Button } from "@/components/button";
import { Modal } from "@/components/Modal";
import { Input } from "@/components/Input";
import { Notice } from "@/components/Notice";
import { useFuzzy, type HighlightRange } from "@/lib/fuzzy";
import { formatBytes, formatCount, formatDuration, formatNumber, formatRate } from "@/lib/format";

export const Route = createFileRoute("/clusters/$cluster/topics/")({
  component: TopicsPage,
});

function TopicsPage() {
  const { cluster, clusters } = useCluster();
  const clusterInfo = useMemo(
    () => clusters?.find((c) => c.name === cluster),
    [clusters, cluster],
  );

  const topicsQuery = useQuery({
    queryKey: ["topics", cluster],
    queryFn: () => fetchTopics(cluster!),
    enabled: !!cluster,
  });

  return (
    <div className="space-y-5 p-6">
      <TopicsPageInner
        cluster={cluster}
        clusterInfo={clusterInfo}
        topics={topicsQuery.data}
        isLoading={topicsQuery.isLoading}
        isError={topicsQuery.isError}
        error={topicsQuery.error as Error | undefined}
        onRetry={() => topicsQuery.refetch()}
      />
    </div>
  );
}

function TopicsPageInner({
  cluster,
  clusterInfo,
  topics,
  isLoading,
  isError,
  error,
  onRetry,
}: {
  cluster: string | null;
  clusterInfo: ClusterInfo | undefined;
  topics: TopicInfo[] | undefined;
  isLoading: boolean;
  isError: boolean;
  error?: Error;
  onRetry: () => void;
}) {
  const [createOpen, setCreateOpen] = useState(false);
  const { me } = useAuth();
  const rbacAllowsCreate = cluster ? can(me, cluster, "topic", "edit") : false;
  const caps = clusterInfo?.capabilities;
  const createDisabledReason = !cluster
    ? "select a cluster"
    : !rbacAllowsCreate
      ? "forbidden by RBAC policy"
      : caps?.create_topic === false
        ? (caps?.errors?.create_topic ?? "CREATE on TOPIC required")
        : undefined;

  const visible = topics?.filter((t) => !t.is_internal) ?? [];
  const partitionSum = visible.reduce((s, t) => s + t.partitions, 0);
  const totalSize = visible.reduce(
    (s, t) => (t.size_bytes != null ? s + t.size_bytes : s),
    0,
  );
  const anySize = visible.some((t) => t.size_bytes != null);
  const subtitle = !topics || !cluster
    ? "—"
    : anySize
      ? `${visible.length} topics · ${partitionSum} partitions · ${formatBytes(totalSize)} retained`
      : `${visible.length} topics · ${partitionSum} partitions`;

  const createReasonId = "topics-create-disabled-reason";
  return (
    <>
      <PageHeader
        eyebrow={
          <>
            <span className="font-mono normal-case tracking-normal">{cluster ?? "—"}</span>{" "}
            <span aria-hidden>›</span> Topics
          </>
        }
        title="Topics"
        subtitle={subtitle}
        actions={
          <>
            <Button
              variant="primary"
              size="sm"
              onClick={() => setCreateOpen(true)}
              disabled={!!createDisabledReason}
              aria-describedby={createDisabledReason ? createReasonId : undefined}
            >
              + New topic
            </Button>
            {createDisabledReason ? (
              <span id={createReasonId} className="sr-only">
                {createDisabledReason}
              </span>
            ) : null}
          </>
        }
      />

      {!cluster && (
        <EmptyState
          icon={Boxes}
          title="No cluster selected"
          description="Pick a cluster from the header to browse its topics."
        />
      )}

      {cluster && isError && (
        <ErrorState
          title="Failed to load topics"
          detail={error?.message}
          onRetry={onRetry}
        />
      )}

      {cluster && !isError && (
        <TopicsBody
          cluster={cluster}
          topics={topics}
          isLoading={isLoading}
          createOpen={createOpen}
          setCreateOpen={setCreateOpen}
          createDisabledReason={createDisabledReason}
        />
      )}
    </>
  );
}

interface BodyProps {
  cluster: string;
  topics: TopicInfo[] | undefined;
  isLoading: boolean;
  createOpen: boolean;
  setCreateOpen: (v: boolean) => void;
  createDisabledReason: string | undefined;
}

function TopicsBody({ cluster, topics, isLoading, createOpen, setCreateOpen, createDisabledReason }: BodyProps) {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [showInternal, setShowInternal] = useState(false);

  const visibleTopics = useMemo(() => {
    if (!topics) return [];
    return topics
      .filter((tp) => (showInternal ? true : !tp.is_internal))
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [topics, showInternal]);

  const fuzzy = useFuzzy(visibleTopics, { keys: ["name"], query: q });
  const filtered = fuzzy.results;

  const showSize = useMemo(
    () => visibleTopics.some((t) => t.size_bytes !== null && t.size_bytes !== undefined),
    [visibleTopics],
  );
  const showRetention = useMemo(
    () =>
      visibleTopics.some(
        (t) => t.retention_ms !== null && t.retention_ms !== undefined,
      ),
    [visibleTopics],
  );

  // `createDisabledReason` and `setCreateOpen` are owned by the parent
  // (which mounts the button in <PageHeader actions/>). We only consume
  // them here to gate the modal mount and to render the inline reason.
  void createDisabledReason;
  return (
    <>
      <Toolbar
        search={
          <SearchInput
            value={q}
            onChange={setQ}
            placeholder="Filter topics by name…"
            ariaLabel="Filter topics"
            count={{ visible: filtered.length, total: visibleTopics.length }}
          />
        }
        filters={
          <>
            <PlaceholderFilter label="Retention: any" />
            <PlaceholderFilter label="Partitions: any" />
            <label className="flex h-9 cursor-pointer items-center gap-2 rounded-md border border-border bg-panel px-3 text-xs text-muted">
              <input
                type="checkbox"
                checked={showInternal}
                onChange={(e) => setShowInternal(e.target.checked)}
                className="h-3.5 w-3.5 accent-accent"
              />
              Show internal
            </label>
          </>
        }
      />

      {isLoading ? (
        <TopicsSkeleton />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={Boxes}
          title={q ? "No topics match your filter" : "No topics yet"}
          description={q ? undefined : "Create your first topic or toggle “Show internal”."}
        />
      ) : (
        <DataTable>
          <DataTableHead>
            <tr>
              <DataTableTh>Topic</DataTableTh>
              <DataTableTh align="right">Partitions</DataTableTh>
              <DataTableTh align="right">RF</DataTableTh>
              <DataTableTh align="right">Messages</DataTableTh>
              {showSize && <DataTableTh align="right">Size</DataTableTh>}
              <DataTableTh align="right">Rate</DataTableTh>
              <DataTableTh align="right">Lag</DataTableTh>
              {showRetention && <DataTableTh>Retention</DataTableTh>}
            </tr>
          </DataTableHead>
          <tbody>
            {filtered.map((t) => (
              <TopicRow
                key={t.name}
                cluster={cluster}
                topic={t}
                showSize={showSize}
                showRetention={showRetention}
                nameHighlight={fuzzy.rangesFor(t, "name")}
                onClick={() =>
                  navigate({
                    to: "/clusters/$cluster/topics/$topic",
                    params: { cluster, topic: t.name },
                  })
                }
              />
            ))}
          </tbody>
        </DataTable>
      )}

      {createOpen && (
        <CreateTopicModal cluster={cluster} onClose={() => setCreateOpen(false)} />
      )}
    </>
  );
}

function TopicRow({
  cluster: _cluster,
  topic,
  onClick,
  nameHighlight,
  showSize,
  showRetention,
}: {
  cluster: string;
  topic: TopicInfo;
  onClick: () => void;
  nameHighlight?: readonly HighlightRange[];
  showSize: boolean;
  showRetention: boolean;
}) {
  return (
    <DataTableRow className="cursor-pointer" onClick={onClick}>
      <td className="px-4 py-2.5">
        <div className="flex items-center gap-2">
          <span className="font-mono text-[13px] tabular-nums text-text">
            <Highlight text={topic.name} ranges={nameHighlight ?? []} />
          </span>
          {topic.is_internal && <Tag>INTERNAL</Tag>}
        </div>
      </td>
      {/* TODO(backend): per-topic owner not exposed yet */}
      <td className="px-4 py-2.5 text-right font-mono text-[13px] tabular-nums">
        {topic.partitions}
      </td>
      <td className="px-4 py-2.5 text-right font-mono text-[13px] tabular-nums">
        {topic.replication_factor}
      </td>
      {/* TODO(backend): per-topic msg rate (rate_per_sec aggregated server-side) */}
      <MetricCell align="right" value={topic.messages} format={formatCount} />
      {showSize && (
        <MetricCell align="right" value={topic.size_bytes} format={formatBytes} />
      )}
      <MetricCell align="right" value={topic.rate_per_sec} format={formatRate} />
      <MetricCell align="right" value={topic.lag} format={formatCount} />
      {showRetention && (
        <MetricCell value={topic.retention_ms} format={formatDuration} />
      )}
    </DataTableRow>
  );
}

function MetricCell({
  value,
  format,
  align = "left",
}: {
  value: number | null | undefined;
  format: (n: number) => string;
  align?: "left" | "right";
}) {
  const known = value !== null && value !== undefined;
  return (
    <td
      className={clsx(
        "px-4 py-2.5 font-mono text-[13px] tabular-nums",
        known ? "text-text" : "text-subtle-text",
        align === "right" && "text-right",
      )}
    >
      {known ? format(value) : "—"}
    </td>
  );
}

function PlaceholderFilter({ label }: { label: string }) {
  // Placeholder filter — not wired up yet. The "coming soon" reason is
  // exposed via `aria-label` so screen readers announce it instead of
  // relying on hover-only `title`.
  return (
    <button
      type="button"
      disabled
      aria-label={`${label} (filter coming soon)`}
      className="flex h-9 items-center gap-1.5 rounded-md border border-border bg-panel px-3 text-xs text-muted"
    >
      {label}
      <ChevronDown className="h-3.5 w-3.5" />
    </button>
  );
}

function TopicsSkeleton() {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-panel">
      {[0, 1, 2, 3, 4, 5].map((i) => (
        <div key={i} className="flex items-center gap-4 border-t border-border px-4 py-3 first:border-t-0">
          <div className="h-3 w-48 animate-pulse rounded bg-subtle" />
          <div className="h-3 w-20 animate-pulse rounded bg-subtle" />
          <div className="ml-auto h-3 w-16 animate-pulse rounded bg-subtle" />
          <div className="h-3 w-16 animate-pulse rounded bg-subtle" />
        </div>
      ))}
    </div>
  );
}

function CreateTopicModal({
  cluster,
  onClose,
}: {
  cluster: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [partitions, setPartitions] = useState(1);
  const [rf, setRF] = useState(1);
  const [configRows, setConfigRows] = useState<{ k: string; v: string }[]>([]);
  const [err, setErr] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: async () => {
      const configs: Record<string, string> = {};
      for (const { k, v } of configRows) {
        if (k.trim()) configs[k.trim()] = v;
      }
      await createTopic(cluster, {
        name: name.trim(),
        partitions,
        replication_factor: rf,
        configs: Object.keys(configs).length ? configs : undefined,
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["topics", cluster] });
      onClose();
    },
    onError: (e: Error) => setErr(e.message),
  });

  const disabled = !name.trim() || mut.isPending;
  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={
        <span className="flex flex-col">
          <span className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            Create topic on
          </span>
          <span className="font-mono text-[13px] font-semibold">{cluster}</span>
        </span>
      }
      actions={
        <>
          <span className="mr-auto text-xs text-muted">
            {partitions} × {formatNumber(partitions)} · RF {rf}
          </span>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={disabled}
            onClick={() => {
              setErr(null);
              mut.mutate();
            }}
          >
            {mut.isPending ? "Creating…" : "Create"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <label className="block">
          <span className="text-xs font-semibold uppercase tracking-wider text-muted">
            Name
          </span>
          <Input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="my-topic"
            className="mt-1 font-mono"
          />
        </label>
        <div className="grid grid-cols-2 gap-4">
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Partitions
            </span>
            <Input
              type="number"
              min={1}
              value={partitions}
              onChange={(e) => setPartitions(Math.max(1, Number(e.target.value)))}
              className="mt-1"
            />
          </label>
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Replication factor
            </span>
            <Input
              type="number"
              min={1}
              value={rf}
              onChange={(e) => setRF(Math.max(1, Number(e.target.value)))}
              className="mt-1"
            />
          </label>
        </div>
        <div>
          <div className="mb-1 flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Configs (optional)
            </span>
            <button
              type="button"
              onClick={() => setConfigRows((r) => [...r, { k: "", v: "" }])}
              className="text-xs text-muted transition-colors hover:text-text"
            >
              + add
            </button>
          </div>
          {configRows.length === 0 ? (
            <div className="rounded-md border border-dashed border-border p-2 text-center text-xs text-subtle-text">
              no overrides (broker defaults apply)
            </div>
          ) : (
            <div className="space-y-1.5">
              {configRows.map((row, i) => (
                <div key={i} className="flex gap-2">
                  <Input
                    value={row.k}
                    onChange={(e) =>
                      setConfigRows((rs) =>
                        rs.map((r, idx) => (idx === i ? { ...r, k: e.target.value } : r)),
                      )
                    }
                    placeholder="retention.ms"
                    className="w-1/2 font-mono"
                  />
                  <Input
                    value={row.v}
                    onChange={(e) =>
                      setConfigRows((rs) =>
                        rs.map((r, idx) => (idx === i ? { ...r, v: e.target.value } : r)),
                      )
                    }
                    placeholder="604800000"
                    className="flex-1 font-mono"
                  />
                  <button
                    type="button"
                    onClick={() => setConfigRows((rs) => rs.filter((_, idx) => idx !== i))}
                    className="px-2 text-xs text-subtle-text transition-colors hover:text-danger"
                    aria-label={`Remove config row ${i + 1}`}
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
        {err && <Notice intent="danger">{err}</Notice>}
      </div>
    </Modal>
  );
}
