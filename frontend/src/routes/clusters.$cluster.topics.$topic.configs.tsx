import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { useId, useMemo, useState } from "react";
import { alterTopicConfigs, can, type Capabilities, type TopicConfigEntry } from "@/lib/api";
import { useAuth } from "@/auth/hooks";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/Input";
import { Modal } from "@/components/ui/Modal";
import { Notice } from "@/components/ui/Notice";
import { StatusBox, StatusIcon } from "@/components/ui/StatusIcon";
import { clusterQueries } from "@/lib/queries/clusters";
import { topicQueries } from "@/lib/queries/topics";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/configs")({
  component: ConfigsTab,
});

function ConfigsTab() {
  const { cluster, topic } = Route.useParams();

  const clustersQuery = useQuery(clusterQueries.list());
  const caps = useMemo(
    () => clustersQuery.data?.find((c) => c.name === cluster)?.capabilities ?? undefined,
    [clustersQuery.data, cluster],
  );

  const detailQuery = useQuery({
    ...topicQueries.detail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: (query) =>
      query.state.data?.configs_error === "unauthorized" ? false : 5_000,
  });

  if (detailQuery.isLoading && cluster) {
    return <div className="text-sm text-muted">Loading…</div>;
  }

  if (!detailQuery.data) return null;

  const configsError = detailQuery.data.configs_error;
  const noticeIntent = configsError === "unauthorized" ? "warning" : "danger";
  const noticeTitle =
    configsError === "unauthorized" ? "Permission missing" : "Configs unavailable";
  const noticeBody =
    configsError === "unauthorized"
      ? "kafkito cannot read this topic's configuration because the broker denied DescribeConfigs. Ask the cluster admin to grant DescribeConfigs on this topic (or topic prefix) to the API key in use."
      : "The broker returned an error while reading this topic's configuration. Retries are made every few seconds; check the kafkito logs for details.";

  return (
    <div className="space-y-4">
      {configsError && (
        <Notice intent={noticeIntent} title={noticeTitle}>
          {noticeBody}
        </Notice>
      )}
      <ConfigsTable
        cluster={cluster}
        topic={topic}
        configs={detailQuery.data.configs}
        caps={caps}
      />
    </div>
  );
}

function ConfigsTable({
  cluster,
  topic,
  configs,
  caps,
}: {
  cluster: string;
  topic: string;
  configs: TopicConfigEntry[];
  caps?: Capabilities;
}) {
  const [showDefaults, setShowDefaults] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const filtered = useMemo(
    () => (showDefaults ? configs : configs.filter((c) => !c.is_default)),
    [configs, showDefaults],
  );
  const disabled = caps !== undefined && caps.describe_configs === false;
  const { me } = useAuth();
  const rbacAllowsEdit = can(me, cluster, "topic", "edit", topic);
  const canAlter = caps?.alter_configs !== false && rbacAllowsEdit;
  const alterReason = !rbacAllowsEdit
    ? "forbidden by RBAC policy"
    : (caps?.errors?.alter_configs ?? "ALTER_CONFIGS on TOPIC required");
  const editBlocked = !disabled && !canAlter;
  const editReasonId = useId();

  return (
    <Card flush className={disabled ? "bg-subtle opacity-75" : undefined} aria-disabled={disabled}>
      <div className="flex items-center justify-between border-b border-border p-3">
        <div className="flex items-center gap-2 text-sm font-semibold">
          Configuration
          {disabled && (
            <span
              title={
                caps?.errors?.describe_configs
                  ? `Disabled — broker returned ${caps.errors.describe_configs}. Grant DESCRIBE_CONFIGS on TOPIC.`
                  : "Disabled — the configured Kafka user lacks DESCRIBE_CONFIGS on TOPIC."
              }
              className="inline-flex items-center gap-1 rounded-sm bg-tint-amber-bg px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-tint-amber-fg"
            >
              ⚠ DISABLED
            </span>
          )}
        </div>
        <div className="flex items-center gap-3">
          <label
            className={[
              "flex items-center gap-1.5 text-xs text-muted",
              disabled ? "pointer-events-none opacity-50" : "",
            ].join(" ")}
          >
            <input
              type="checkbox"
              checked={showDefaults}
              onChange={(e) => setShowDefaults(e.target.checked)}
              className="h-3.5 w-3.5"
              disabled={disabled}
            />
            Show defaults ({configs.length})
          </label>
          {editBlocked && (
            <span id={editReasonId} className="text-xs text-muted">
              Read-only: {alterReason}
            </span>
          )}
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setEditOpen(true)}
            disabled={disabled || !canAlter}
            aria-describedby={editBlocked ? editReasonId : undefined}
          >
            Edit…
          </Button>
        </div>
      </div>
      {disabled ? (
        <div className="p-6 text-center text-sm text-muted">
          Topic configuration is hidden because the configured Kafka user lacks the{" "}
          <code className="font-mono text-xs">DESCRIBE_CONFIGS</code> permission on topics on this
          cluster.
          {caps?.errors?.describe_configs && (
            <div className="mt-2 font-mono text-[11px] text-subtle-text">
              {caps.errors.describe_configs}
            </div>
          )}
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-subtle text-left text-xs uppercase tracking-wider text-muted">
            <tr>
              <th className="px-4 py-2 font-semibold">Key</th>
              <th className="px-4 py-2 font-semibold">Value</th>
              <th className="px-4 py-2 font-semibold">Source</th>
            </tr>
          </thead>
          <tbody>
            {filtered.length === 0 ? (
              <tr>
                <td colSpan={3} className="px-4 py-8 text-center text-subtle-text">
                  {showDefaults ? "No configs." : "No non-default overrides."}
                </td>
              </tr>
            ) : (
              filtered.map((c) => (
                <tr key={c.name} className="border-b border-border last:border-0">
                  <td className="px-4 py-2 font-mono text-xs">{c.name}</td>
                  <td className="px-4 py-2 font-mono text-xs">
                    {c.sensitive ? <span className="text-subtle-text">•••</span> : c.value}
                  </td>
                  <td className="px-4 py-2 text-xs text-muted">
                    {c.source || (c.is_default ? "default" : "override")}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      )}
      {editOpen && (
        <EditConfigsModal
          cluster={cluster}
          topic={topic}
          configs={configs}
          onClose={() => setEditOpen(false)}
        />
      )}
    </Card>
  );
}

function EditConfigsModal({
  cluster,
  topic,
  configs,
  onClose,
}: {
  cluster: string;
  topic: string;
  configs: TopicConfigEntry[];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const overrides = useMemo(() => configs.filter((c) => !c.is_default && !c.sensitive), [configs]);
  const [rows, setRows] = useState<
    Array<{ name: string; value: string; op: "set" | "delete" | "keep" }>
  >(() => overrides.map((c) => ({ name: c.name, value: c.value, op: "keep" })));
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const [results, setResults] = useState<Array<{
    name: string;
    op: string;
    error?: string;
  }> | null>(null);

  const mut = useMutation({
    mutationFn: async () => {
      const set: Record<string, string> = {};
      const del: string[] = [];
      for (const r of rows) {
        if (r.op === "set") set[r.name] = r.value;
        if (r.op === "delete") del.push(r.name);
      }
      if (newKey.trim()) set[newKey.trim()] = newValue;
      return alterTopicConfigs(cluster, topic, {
        set: Object.keys(set).length ? set : undefined,
        delete: del.length ? del : undefined,
      });
    },
    onSuccess: (data) => {
      setResults(data.results);
      qc.invalidateQueries({ queryKey: topicQueries.detail(cluster, topic).queryKey });
    },
  });

  const hasChanges = rows.some((r) => r.op !== "keep") || (newKey.trim() !== "" && newValue !== "");

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={
        <>
          Edit configuration — <span className="font-mono">{topic}</span>
        </>
      }
      onSubmit={() => {
        if (hasChanges && !mut.isPending) mut.mutate();
      }}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
          <Button type="submit" disabled={!hasChanges} loading={mut.isPending}>
            {mut.isPending ? "Applying…" : "Apply"}
          </Button>
        </>
      }
    >
      <div className="space-y-2">
        <div className="text-xs font-medium uppercase tracking-wider text-muted">
          Current overrides
        </div>
        {rows.length === 0 ? (
          <div className="text-sm text-muted">No non-default overrides.</div>
        ) : (
          <table className="w-full text-sm">
            <tbody className="divide-y divide-border">
              {rows.map((r, i) => (
                <tr key={r.name}>
                  <td className="py-1 pr-2 font-mono text-xs">{r.name}</td>
                  <td className="py-1 pr-2">
                    <Input
                      aria-label={`Value for ${r.name}`}
                      value={r.value}
                      disabled={r.op === "delete"}
                      onChange={(e) => {
                        const v = e.target.value;
                        setRows((prev) => {
                          const next = [...prev];
                          next[i] = {
                            ...next[i],
                            value: v,
                            op: v !== overrides[i].value ? "set" : "keep",
                          };
                          return next;
                        });
                      }}
                      className="h-8 px-2 font-mono text-xs"
                    />
                  </td>
                  <td className="py-1 pr-2 text-xs">
                    <select
                      aria-label={`Change for ${r.name}`}
                      value={r.op}
                      onChange={(e) =>
                        setRows((prev) => {
                          const next = [...prev];
                          next[i] = {
                            ...next[i],
                            op: e.target.value as "set" | "delete" | "keep",
                          };
                          return next;
                        })
                      }
                      className="h-8 rounded-md border border-border bg-panel px-2 text-xs text-text hover:border-border-hover"
                    >
                      <option value="keep">keep</option>
                      <option value="set">set</option>
                      <option value="delete">delete (reset)</option>
                    </select>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div className="mt-4 text-xs font-medium uppercase tracking-wider text-muted">
          Add / override key
        </div>
        <div className="flex gap-2">
          <Input
            aria-label="New config key"
            placeholder="key (e.g. retention.ms)"
            value={newKey}
            onChange={(e) => setNewKey(e.target.value)}
            className="h-8 px-2 font-mono text-xs"
          />
          <Input
            aria-label="New config value"
            placeholder="value"
            value={newValue}
            onChange={(e) => setNewValue(e.target.value)}
            className="h-8 px-2 font-mono text-xs"
          />
        </div>
      </div>

      {mut.error && (
        <StatusBox intent="danger" className="mt-3 px-3 py-2">
          {(mut.error as Error).message}
        </StatusBox>
      )}
      {results && (
        <div className="mt-3">
          <div className="text-xs font-medium uppercase tracking-wider text-muted">Results</div>
          <ul className="mt-1 space-y-0.5 text-sm">
            {results.map((r, i) => (
              <li key={i} className="flex items-center justify-between">
                <span className="font-mono text-xs">
                  {r.op} {r.name}
                </span>
                {r.error ? (
                  <span className="inline-flex items-center gap-1 text-xs text-danger">
                    <StatusIcon intent="danger" className="h-3 w-3" />
                    {r.error}
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1 text-xs text-success">
                    <StatusIcon intent="success" className="h-3 w-3" />
                    ok
                  </span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </Modal>
  );
}
