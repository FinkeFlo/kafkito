import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import {
  fetchGroups,
  fetchGroupDetail,
  resetGroupOffsets,
  deleteGroup,
  type ClusterInfo,
  type GroupInfo,
  type GroupDetail,
  type ResetStrategy,
} from "@/lib/api";
import { useCluster } from "@/lib/use-cluster";
import { useMemo, useState } from "react";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";
import { Toolbar } from "@/components/Toolbar";
import { Modal } from "@/components/Modal";
import { Button } from "@/components/button";
import { Input } from "@/components/Input";
import { PageHeader } from "@/components/page-header";
import { StateBadge } from "@/components/StateBadge";
import { LagBadge } from "@/components/lag-badge";
import { MonoId } from "@/components/mono-id";
import { SearchInput } from "@/components/search-input";
import { Highlight } from "@/components/highlight";
import { useFuzzy } from "@/lib/fuzzy";
import { useTranslation } from "react-i18next";
import { Users } from "lucide-react";

export const Route = createFileRoute("/clusters/$cluster/groups/")({
  validateSearch: (s: Record<string, unknown>) => ({
    group: typeof s.group === "string" ? s.group : undefined,
  }),
  component: GroupsPage,
});

function GroupsPage() {
  const { t } = useTranslation(["groups", "common"]);
  const { group } = Route.useSearch();
  const navigate = Route.useNavigate();
  const { cluster, clusters } = useCluster();
  const selected = cluster;
  const setGroup = (g: string | undefined) =>
    navigate({ search: { group: g }, replace: true });

  const selectedInfo = useMemo(
    () => clusters?.find((c) => c.name === selected),
    [clusters, selected],
  );

  const groupsQuery = useQuery({
    queryKey: ["groups", selected],
    queryFn: () => fetchGroups(selected!),
    enabled: !!selected,
    refetchInterval: 10_000,
  });

  const capDisabled =
    selectedInfo?.capabilities?.list_groups === false;

  const groups = groupsQuery.data ?? [];
  const counts = {
    stable: groups.filter((g) => g.state.toLowerCase() === "stable").length,
    rebalancing: groups.filter((g) => /rebalance/i.test(g.state)).length,
    dead: groups.filter((g) => g.state.toLowerCase() === "dead").length,
    empty: groups.filter((g) => g.state.toLowerCase() === "empty").length,
  };

  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={
          <>
            <span className="font-mono normal-case tracking-normal">{selected ?? "—"}</span>{" "}
            <span aria-hidden>›</span> Consumer groups
          </>
        }
        title="Consumer groups"
        subtitle={
          groupsQuery.isLoading
            ? t("groups:subtitle")
            : `${groups.length} ${groups.length === 1 ? "group" : "groups"} · ${counts.stable} stable · ${counts.rebalancing} rebalancing · ${counts.dead} dead`
        }
      />

      {capDisabled && (
        <Notice intent="warning" title="Consumer groups unavailable">
          The configured Kafka user lacks <code className="font-mono">DESCRIBE</code>{" "}
          on <code className="font-mono">GROUP:*</code>. Granting it will
          enable this view.
        </Notice>
      )}

      <GroupsTable
        cluster={selected}
        clusterInfo={selectedInfo}
        groups={groupsQuery.data}
        isLoading={groupsQuery.isLoading}
        error={groupsQuery.error as Error | null}
        selectedGroup={group}
        onSelect={setGroup}
      />

      {selected && group && (
        <GroupDetailPanel cluster={selected} group={group} />
      )}
    </div>
  );
}

function GroupsTable({
  cluster,
  clusterInfo,
  groups,
  isLoading,
  error,
  selectedGroup,
  onSelect,
}: {
  cluster: string | null;
  clusterInfo: ClusterInfo | undefined;
  groups: GroupInfo[] | undefined;
  isLoading: boolean;
  error: Error | null;
  selectedGroup: string | undefined;
  onSelect: (g: string | undefined) => void;
}) {
  const { t } = useTranslation(["groups", "common"]);
  const [q, setQ] = useState("");
  const [lagOnly, setLagOnly] = useState(false);
  const preFiltered = useMemo(() => {
    if (!groups) return [];
    return lagOnly
      ? groups.filter((g) => g.lag_known && g.lag > 0)
      : groups;
  }, [groups, lagOnly]);

  const fuzzy = useFuzzy(preFiltered, { keys: ["group_id"], query: q });
  const filtered = fuzzy.results;

  const columns = useMemo<DataTableColumn<GroupInfo>[]>(
    () => [
      {
        id: "group_id",
        header: t("groups:columns.groupId"),
        sortValue: (r) => r.group_id,
        cell: (r) => {
          const ranges = fuzzy.rangesFor(r, "group_id");
          return (
            <span className="font-mono text-[13px] tabular-nums">
              <Highlight text={r.group_id} ranges={ranges} />
            </span>
          );
        },
      },
      {
        id: "state",
        header: t("groups:columns.state"),
        sortValue: (r) => r.state,
        cell: (r) => <StateBadge state={r.state} />,
        className: "w-36",
      },
      {
        id: "members",
        header: t("groups:columns.members"),
        align: "right",
        className: "tabular-nums w-24",
        sortValue: (r) => r.members,
        cell: (r) => r.members,
      },
      {
        id: "topics",
        header: t("groups:columns.topics"),
        align: "right",
        className: "tabular-nums w-20",
        sortValue: (r) => r.topics ?? 0,
        cell: (r) => r.topics ?? 0,
      },
      {
        id: "lag",
        header: t("groups:columns.lag"),
        align: "right",
        className: "w-28",
        sortValue: (r) => (r.lag_known ? r.lag : -1),
        cell: (r) => {
          if (r.lag_known) return <LagBadge value={r.lag} />;
          const reasonId = `lag-unknown-${r.group_id}`;
          const reason = r.error ?? "lag unavailable";
          return (
            <>
              <span
                aria-describedby={reasonId}
                className="text-subtle-text"
              >
                ?
              </span>
              <span id={reasonId} className="sr-only">
                {reason}
              </span>
            </>
          );
        },
      },
      {
        id: "coordinator",
        header: t("groups:columns.coordinator"),
        className: "w-28 font-mono text-[13px] tabular-nums",
        sortValue: (r) => r.coordinator_id,
        cell: (r) => <span className="text-muted">{r.coordinator_id}</span>,
      },
    ],
    [t, fuzzy],
  );

  if (!cluster) return null;
  if (error) {
    return (
      <Notice intent="danger" title="Failed to load groups">
        {error.message}
      </Notice>
    );
  }

  const limited =
    !!groups &&
    groups.length === 0 &&
    clusterInfo?.capabilities &&
    (!clusterInfo.capabilities.list_groups ||
      Object.keys(clusterInfo.capabilities.errors ?? {}).length > 0);

  const total = groups?.length ?? 0;
  const empty = (
    <EmptyState
      icon={<Users className="h-5 w-5" />}
      title={q ? t("groups:empty.noMatch") : t("groups:empty.title")}
      description={q ? undefined : t("groups:empty.description")}
    />
  );

  return (
    <>
      {limited && (
        <Notice intent="warning" title={t("groups:limited.title")} className="mb-3">
          {t("groups:limited.description")}
        </Notice>
      )}
      <Toolbar
        search={
          <SearchInput
            value={q}
            onChange={setQ}
            placeholder={t("groups:filter.placeholder")}
            ariaLabel={t("groups:filter.placeholder")}
            count={{ visible: filtered.length, total: preFiltered.length }}
          />
        }
        filters={
          <label className="flex items-center gap-1.5 text-sm text-muted">
            <input
              type="checkbox"
              checked={lagOnly}
              onChange={(e) => setLagOnly(e.target.checked)}
              className="h-4 w-4"
            />
            {t("groups:filter.lagOnly")}
          </label>
        }
      />

      <DataTable<GroupInfo>
        columns={columns}
        rows={filtered}
        rowKey={(r) => r.group_id}
        isLoading={isLoading}
        emptyState={empty}
        caption={t("common:filters.showingOf", { count: filtered.length, total })}
        onRowClick={(r) => onSelect(r.group_id === selectedGroup ? undefined : r.group_id)}
      />
    </>
  );
}

function GroupDetailPanel({ cluster, group }: { cluster: string; group: string }) {
  const q = useQuery({
    queryKey: ["group", cluster, group],
    queryFn: () => fetchGroupDetail(cluster, group),
    refetchInterval: 5_000,
  });
  if (q.isLoading) {
    return (
      <div className="rounded-xl border border-border bg-panel p-6 text-sm text-muted">
        Loading group detail…
      </div>
    );
  }
  if (q.error) {
    return (
      <Notice intent="danger" title="Failed to load group">
        {(q.error as Error).message}
      </Notice>
    );
  }
  if (!q.data) return null;
  return <GroupDetailBody cluster={cluster} detail={q.data} />;
}

function GroupDetailBody({ cluster, detail }: { cluster: string; detail: GroupDetail }) {
  const byTopic = useMemo(() => {
    const m = new Map<string, GroupDetail["offsets"]>();
    for (const o of detail.offsets) {
      if (!m.has(o.topic)) m.set(o.topic, []);
      m.get(o.topic)!.push(o);
    }
    return Array.from(m.entries()).sort((a, b) => a[0].localeCompare(b[0]));
  }, [detail.offsets]);

  return (
    <div className="space-y-4">
      <div className="rounded-xl border border-border bg-panel p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <div className="font-mono text-sm font-semibold">{detail.group_id}</div>
            <div className="mt-1 flex items-center gap-2 text-xs text-muted">
              <StateBadge state={detail.state} />
              <span>· coordinator {detail.coordinator_id}</span>
              {detail.protocol && <span>· {detail.protocol}</span>}
              {detail.protocol_type && detail.protocol_type !== "consumer" && (
                <span className="rounded bg-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-muted">
                  {detail.protocol_type}
                </span>
              )}
            </div>
          </div>
          <div className="grid grid-cols-3 gap-4 text-right text-xs">
            <Metric label="Members" value={detail.members.length} />
            <Metric label="Topics" value={byTopic.length} />
            <Metric
              label="Total lag"
              value={detail.lag_known ? formatNum(detail.lag) : "?"}
              highlight={detail.lag_known && detail.lag > 0}
            />
          </div>
        </div>
        <GroupActions cluster={cluster} detail={detail} />
      </div>

      <div className="rounded-xl border border-border bg-panel">
        <div className="border-b border-border p-3 text-sm font-semibold">
          Members
        </div>
        {detail.members.length === 0 ? (
          <div className="p-6 text-center text-sm text-subtle-text">
            No active members (group is empty).
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-subtle text-left text-xs uppercase tracking-wider text-muted">
              <tr>
                <th className="px-4 py-2 font-semibold">Client</th>
                <th className="px-4 py-2 font-semibold">Host</th>
                <th className="px-4 py-2 font-semibold">Member ID</th>
                <th className="px-4 py-2 font-semibold">Assignments</th>
              </tr>
            </thead>
            <tbody>
              {detail.members.map((m) => (
                <tr key={m.member_id} className="border-b border-border last:border-0">
                  <td className="max-w-[340px] px-4 py-2 text-xs">
                    <div className="flex items-center gap-2">
                      <MonoId value={m.client_id} placeholder="—" />
                      {m.instance_id && (
                        <span className="shrink-0 rounded bg-subtle px-1 py-0.5 text-[10px] text-muted">
                          static:{m.instance_id}
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted">
                    {m.client_host}
                  </td>
                  <td className="max-w-[260px] px-4 py-2 text-xs">
                    <MonoId value={m.member_id} muted />
                  </td>
                  <td className="px-4 py-2 text-xs">
                    {!m.assignments || m.assignments.length === 0 ? (
                      <span className="text-subtle-text">—</span>
                    ) : (
                      m.assignments.map((a) => (
                        <Link
                          key={a.topic}
                          to="/clusters/$cluster/topics/$topic"
                          params={{ cluster, topic: a.topic }}
                          className="mr-1 inline-flex items-center rounded bg-subtle px-1.5 py-0.5 font-mono hover:bg-hover"
                        >
                          {a.topic}
                          <span className="ml-1 text-muted">
                            [{a.partitions.length}]
                          </span>
                        </Link>
                      ))
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="rounded-xl border border-border bg-panel">
        <div className="border-b border-border p-3 text-sm font-semibold">
          Offsets
        </div>
        {byTopic.length === 0 ? (
          <div className="p-6 text-center text-sm text-subtle-text">
            No committed offsets.
          </div>
        ) : (
          byTopic.map(([topic, offsets]) => {
            const topicLag = offsets.reduce(
              (s, o) => (o.lag >= 0 ? s + o.lag : s),
              0,
            );
            return (
              <div key={topic} className="border-b border-border last:border-0">
                <div className="flex items-center justify-between bg-subtle/60 px-4 py-2 text-xs">
                  <Link
                    to="/clusters/$cluster/topics/$topic"
                    params={{ cluster, topic }}
                    className="font-mono font-semibold text-accent hover:underline"
                  >
                    {topic}
                  </Link>
                  <div className="flex items-center gap-3 text-muted">
                    <span>{offsets.length} partitions</span>
                    {topicLag > 0 && (
                      <span className="font-semibold text-warning">
                        lag {formatNum(topicLag)}
                      </span>
                    )}
                  </div>
                </div>
                <table className="w-full text-sm">
                  <thead className="text-left text-[10px] uppercase tracking-wider text-subtle-text">
                    <tr>
                      <th className="px-4 py-1.5 font-semibold">Partition</th>
                      <th className="px-4 py-1.5 text-right font-semibold">Offset</th>
                      <th className="px-4 py-1.5 text-right font-semibold">Log end</th>
                      <th className="px-4 py-1.5 text-right font-semibold">Lag</th>
                      <th className="px-4 py-1.5 font-semibold">Consumer</th>
                      <th className="px-4 py-1.5 font-semibold">Host</th>
                    </tr>
                  </thead>
                  <tbody>
                    {offsets
                      .slice()
                      .sort((a, b) => a.partition - b.partition)
                      .map((o) => {
                        const at = o.assigned_to || "";
                        const atIdx = at.indexOf("@");
                        const clientId = atIdx >= 0 ? at.slice(0, atIdx) : at;
                        const host = atIdx >= 0 ? at.slice(atIdx + 1) : "";
                        return (
                        <tr
                          key={o.partition}
                          className="border-t border-border text-xs"
                        >
                          <td className="px-4 py-1.5 tabular-nums">{o.partition}</td>
                          <td className="px-4 py-1.5 text-right tabular-nums">
                            {o.offset}
                          </td>
                          <td className="px-4 py-1.5 text-right tabular-nums text-muted">
                            {o.log_end >= 0 ? o.log_end : "?"}
                          </td>
                          <td className="px-4 py-1.5 text-right tabular-nums">
                            {o.lag < 0 ? (
                              <span className="text-subtle-text">?</span>
                            ) : o.lag === 0 ? (
                              <span className="text-subtle-text">0</span>
                            ) : (
                              <span className="font-semibold text-warning">
                                {formatNum(o.lag)}
                              </span>
                            )}
                          </td>
                          <td className="max-w-[320px] px-4 py-1.5 text-xs">
                            <MonoId value={clientId} muted placeholder="—" />
                          </td>
                          <td className="px-4 py-1.5 font-mono text-muted">
                            {host || "—"}
                          </td>
                        </tr>
                        );
                      })}
                  </tbody>
                </table>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

function Metric({
  label,
  value,
  highlight,
}: {
  label: string;
  value: string | number;
  highlight?: boolean;
}) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wider text-subtle-text">
        {label}
      </div>
      <div
        className={[
          "text-lg font-semibold tabular-nums",
          highlight ? "text-warning" : "text-text",
        ].join(" ")}
      >
        {value}
      </div>
    </div>
  );
}

function formatNum(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) return (n / 1000).toFixed(1).replace(/\.0$/, "") + "k";
  if (n < 1_000_000_000)
    return (n / 1_000_000).toFixed(1).replace(/\.0$/, "") + "M";
  return (n / 1_000_000_000).toFixed(1).replace(/\.0$/, "") + "B";
}

function GroupActions({
  cluster,
  detail,
}: {
  cluster: string;
  detail: GroupDetail;
}) {
  const qc = useQueryClient();
  const [resetOpen, setResetOpen] = useState(false);
  const [delOpen, setDelOpen] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const isEmpty =
    (detail.state || "").toLowerCase() === "empty" ||
    (detail.state || "").toLowerCase() === "dead";

  const delMut = useMutation({
    mutationFn: () => deleteGroup(cluster, detail.group_id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups", cluster] });
    },
    onError: (e: Error) => setErr(e.message),
  });

  // The "must be Empty/Dead" reason is load-bearing — surface it inline via
  // `aria-describedby` instead of relying on `title=` (mouse-only).
  const reasonId = "group-actions-disabled-reason";
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-border pt-3">
      <Button
        variant="secondary"
        size="sm"
        disabled={!isEmpty}
        onClick={() => setResetOpen(true)}
        aria-describedby={!isEmpty ? reasonId : undefined}
      >
        Reset offsets…
      </Button>
      <Button
        variant="danger"
        size="sm"
        disabled={!isEmpty || delMut.isPending}
        onClick={() => setDelOpen(true)}
        aria-describedby={!isEmpty ? reasonId : undefined}
      >
        {delMut.isPending ? "Deleting…" : "Delete group"}
      </Button>
      <ConfirmDialog
        open={delOpen}
        onOpenChange={setDelOpen}
        title={`Delete consumer group "${detail.group_id}"?`}
        description="This removes the group and all committed offsets. This cannot be undone."
        confirmPhrase={detail.group_id}
        confirmLabel="Delete group"
        variant="danger"
        onConfirm={() => {
          setErr(null);
          delMut.mutate();
        }}
      />
      {!isEmpty && (
        <span id={reasonId} className="text-xs text-muted">
          · active members — stop consumers before managing offsets
        </span>
      )}
      {err && (
        <Notice intent="danger" className="basis-full">
          {err}
        </Notice>
      )}
      {resetOpen && (
        <ResetOffsetsModal
          cluster={cluster}
          detail={detail}
          onClose={() => setResetOpen(false)}
        />
      )}
    </div>
  );
}

function ResetOffsetsModal({
  cluster,
  detail,
  onClose,
}: {
  cluster: string;
  detail: GroupDetail;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const topics = useMemo(() => {
    const s = new Set<string>();
    for (const o of detail.offsets) s.add(o.topic);
    return Array.from(s).sort();
  }, [detail.offsets]);

  const [topic, setTopic] = useState(topics[0] ?? "");
  const [strategy, setStrategy] = useState<ResetStrategy>("earliest");
  const [offset, setOffset] = useState("0");
  const [timestampMs, setTimestampMs] = useState(String(Date.now() - 3600_000));
  const [shift, setShift] = useState("-100");
  const [partSel, setPartSel] = useState<Record<number, boolean>>({});
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<
    | {
        partition: number;
        old_offset: number;
        new_offset: number;
        error?: string;
      }[]
    | null
  >(null);
  const [dryResult, setDryResult] = useState<typeof result>(null);
  const [commitOpen, setCommitOpen] = useState(false);

  const topicParts = useMemo(
    () =>
      detail.offsets
        .filter((o) => o.topic === topic)
        .map((o) => o.partition)
        .sort((a, b) => a - b),
    [detail.offsets, topic],
  );

  const selectedParts = topicParts.filter((p) => partSel[p]);

  const buildBody = (dry_run: boolean) => ({
    topic,
    partitions: selectedParts.length > 0 ? selectedParts : undefined,
    strategy,
    offset: strategy === "offset" ? Number(offset) : undefined,
    timestamp_ms: strategy === "timestamp" ? Number(timestampMs) : undefined,
    shift: strategy === "shift-by" ? Number(shift) : undefined,
    dry_run,
  });

  const dryMut = useMutation({
    mutationFn: () => resetGroupOffsets(cluster, detail.group_id, buildBody(true)),
    onSuccess: (r) => setDryResult(r.results),
    onError: (e: Error) => setErr(e.message),
  });
  const commitMut = useMutation({
    mutationFn: () => resetGroupOffsets(cluster, detail.group_id, buildBody(false)),
    onSuccess: (r) => {
      setResult(r.results);
      qc.invalidateQueries({ queryKey: ["group", cluster, detail.group_id] });
      qc.invalidateQueries({ queryKey: ["groups", cluster] });
    },
    onError: (e: Error) => setErr(e.message),
  });

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={
        <>
          Reset offsets — <span className="font-mono">{detail.group_id}</span>
        </>
      }
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={dryMut.isPending}
            onClick={() => {
              setErr(null);
              setResult(null);
              dryMut.mutate();
            }}
          >
            {dryMut.isPending ? "…" : "Preview"}
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={commitMut.isPending || selectedParts.length === 0}
            onClick={() => setCommitOpen(true)}
          >
            {commitMut.isPending ? "Committing…" : "Commit reset"}
          </Button>
          <ConfirmDialog
            open={commitOpen}
            onOpenChange={setCommitOpen}
            variant="primary"
            title="Commit new offsets?"
            description={`This will overwrite committed offsets for group "${detail.group_id}" on topic "${topic}". Partitions: ${selectedParts.join(",")} (${selectedParts.length} of ${topicParts.length}).`}
            confirmPhrase={detail.group_id}
            confirmLabel="Commit reset"
            onConfirm={() => {
              setErr(null);
              setDryResult(null);
              commitMut.mutate();
            }}
          />
        </>
      }
    >
      <div className="space-y-4 text-sm">
        <div className="grid grid-cols-2 gap-4">
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Topic
            </span>
            <select
              value={topic}
              onChange={(e) => {
                setTopic(e.target.value);
                setPartSel({});
              }}
              className="mt-1 h-9 w-full rounded-md border border-border bg-panel px-2 font-mono text-sm hover:border-border-hover"
            >
              {topics.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </label>
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Strategy
            </span>
            <select
              value={strategy}
              onChange={(e) => setStrategy(e.target.value as ResetStrategy)}
              className="mt-1 h-9 w-full rounded-md border border-border bg-panel px-2 text-sm hover:border-border-hover"
            >
              <option value="earliest">earliest (log start)</option>
              <option value="latest">latest (log end)</option>
              <option value="offset">specific offset</option>
              <option value="timestamp">timestamp (ms)</option>
              <option value="shift-by">shift-by (delta)</option>
            </select>
          </label>
        </div>
        {strategy === "offset" && (
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Offset
            </span>
            <Input
              value={offset}
              onChange={(e) => setOffset(e.target.value)}
              className="mt-1 font-mono"
            />
            <span className="mt-1 block text-xs text-muted">
              Applied to every selected partition. Clamped to [start, end].
            </span>
          </label>
        )}
        {strategy === "timestamp" && (
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Timestamp (epoch ms)
            </span>
            <Input
              value={timestampMs}
              onChange={(e) => setTimestampMs(e.target.value)}
              className="mt-1 font-mono"
            />
          </label>
        )}
        {strategy === "shift-by" && (
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Shift (records, negative allowed)
            </span>
            <Input
              value={shift}
              onChange={(e) => setShift(e.target.value)}
              className="mt-1 font-mono"
            />
          </label>
        )}
        <div>
          <div className="mb-1 flex items-center justify-between text-xs">
            <span className="font-semibold uppercase tracking-wider text-muted">
              Partitions
            </span>
            <div className="flex gap-2 text-xs">
              <button
                type="button"
                onClick={() =>
                  setPartSel(Object.fromEntries(topicParts.map((p) => [p, true])))
                }
                className="text-muted hover:text-text"
              >
                all
              </button>
              <button
                type="button"
                onClick={() => setPartSel({})}
                className="text-muted hover:text-text"
              >
                none
              </button>
            </div>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {topicParts.map((p) => (
              <label
                key={p}
                className={[
                  "flex cursor-pointer items-center gap-1 rounded border px-2 py-0.5 text-xs font-mono",
                  partSel[p]
                    ? "border-accent bg-accent text-accent-foreground"
                    : "border-border bg-panel text-text hover:border-border-hover",
                ].join(" ")}
              >
                <input
                  type="checkbox"
                  checked={!!partSel[p]}
                  onChange={(e) =>
                    setPartSel((s) => ({ ...s, [p]: e.target.checked }))
                  }
                  className="hidden"
                />
                p{p}
              </label>
            ))}
          </div>
          {selectedParts.length === 0 ? (
            <div className="mt-2">
              <Notice intent="warning">Pick at least one partition.</Notice>
            </div>
          ) : (
            <div className="mt-1 text-xs text-muted">
              {`${selectedParts.length} of ${topicParts.length} selected`}
            </div>
          )}
        </div>

        {(dryResult || result) && (
          <div className="rounded-md border border-border bg-subtle p-2 text-xs">
            <div className="mb-1 font-semibold">
              {result ? "Committed" : "Preview (dry-run)"}
            </div>
            <table className="w-full font-mono">
              <thead className="text-[10px] uppercase tracking-wider text-subtle-text">
                <tr>
                  <th className="text-left">partition</th>
                  <th className="text-right">old</th>
                  <th className="text-right">→ new</th>
                  <th className="text-left pl-4">error</th>
                </tr>
              </thead>
              <tbody>
                {(result ?? dryResult)!.map((r) => (
                  <tr key={r.partition}>
                    <td>p{r.partition}</td>
                    <td className="text-right text-muted">
                      {r.old_offset >= 0 ? r.old_offset : "—"}
                    </td>
                    <td className="text-right">
                      {r.new_offset >= 0 ? r.new_offset : "—"}
                    </td>
                    <td className="pl-4 text-danger">{r.error ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {err && (
          <Notice intent="danger">
            {err}
          </Notice>
        )}
      </div>
    </Modal>
  );
}
