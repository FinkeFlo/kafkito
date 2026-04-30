import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import {
  fetchClusters,
  fetchTopicDetail,
  alterTopicConfigs,
  can,
  type Capabilities,
  type TopicConfigEntry,
} from "@/lib/api";
import { useAuth } from "@/auth/hooks";

export const Route = createFileRoute("/topics_/$topic/configs")({
  component: ConfigsTab,
});

function ConfigsTab() {
  const { topic } = Route.useParams();
  const { cluster } = Route.useSearch();

  const clustersQuery = useQuery({
    queryKey: ["clusters"],
    queryFn: fetchClusters,
  });
  const caps = useMemo(
    () =>
      clustersQuery.data?.find((c) => c.name === cluster)?.capabilities ??
      undefined,
    [clustersQuery.data, cluster],
  );

  const detailQuery = useQuery({
    queryKey: ["topic", cluster, topic],
    queryFn: () => fetchTopicDetail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: 5_000,
  });

  if (detailQuery.isLoading && cluster) {
    return <div className="text-sm text-[var(--color-text-muted)]">Loading…</div>;
  }

  if (!detailQuery.data) return null;

  return (
    <ConfigsTable
      cluster={cluster}
      topic={topic}
      configs={detailQuery.data.configs}
      caps={caps}
    />
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

  return (
    <div
      className={[
        "rounded-lg border shadow-sm transition",
        disabled
          ? "border-[var(--color-border)] bg-[var(--color-surface-subtle)] opacity-75"
          : "border-[var(--color-border)] bg-[var(--color-surface-raised)]",
      ].join(" ")}
      aria-disabled={disabled}
    >
      <div className="flex items-center justify-between border-b border-[var(--color-border)] p-3">
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
            "flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]",
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
        <button
          type="button"
          onClick={() => setEditOpen(true)}
          disabled={disabled || !canAlter}
          title={!canAlter && !disabled ? alterReason : undefined}
          className="rounded-md border border-[var(--color-border-strong)] bg-[var(--color-surface-raised)] px-2 py-1 text-xs hover:border-[var(--color-border-strong)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          Edit…
        </button>
        </div>
      </div>
      {disabled ? (
        <div className="p-6 text-center text-sm text-[var(--color-text-muted)]">
          Topic configuration is hidden because the configured Kafka user
          lacks the{" "}
          <code className="font-mono text-xs">DESCRIBE_CONFIGS</code>{" "}
          permission on topics on this cluster.
          {caps?.errors?.describe_configs && (
            <div className="mt-2 font-mono text-[11px] text-[var(--color-text-subtle)]">
              {caps.errors.describe_configs}
            </div>
          )}
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead className="border-b border-[var(--color-border)] bg-[var(--color-surface-subtle)] text-left text-xs uppercase tracking-wider text-[var(--color-text-muted)]">
            <tr>
              <th className="px-4 py-2 font-semibold">Key</th>
              <th className="px-4 py-2 font-semibold">Value</th>
              <th className="px-4 py-2 font-semibold">Source</th>
            </tr>
          </thead>
          <tbody>
            {filtered.length === 0 ? (
              <tr>
                <td colSpan={3} className="px-4 py-8 text-center text-[var(--color-text-subtle)]">
                  {showDefaults ? "No configs." : "No non-default overrides."}
                </td>
              </tr>
            ) : (
              filtered.map((c) => (
                <tr key={c.name} className="border-b border-[var(--color-border)] last:border-0">
                  <td className="px-4 py-2 font-mono text-xs">{c.name}</td>
                  <td className="px-4 py-2 font-mono text-xs">
                    {c.sensitive ? (
                      <span className="text-[var(--color-text-subtle)]">•••</span>
                    ) : (
                      c.value
                    )}
                  </td>
                  <td className="px-4 py-2 text-xs text-[var(--color-text-muted)]">
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
    </div>
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
  const overrides = useMemo(
    () => configs.filter((c) => !c.is_default && !c.sensitive),
    [configs],
  );
  const [rows, setRows] = useState<Array<{ name: string; value: string; op: "set" | "delete" | "keep" }>>(
    () => overrides.map((c) => ({ name: c.name, value: c.value, op: "keep" })),
  );
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const [results, setResults] = useState<Array<{ name: string; op: string; error?: string }> | null>(
    null,
  );

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
      qc.invalidateQueries({ queryKey: ["topic", cluster, topic] });
    },
  });

  const hasChanges =
    rows.some((r) => r.op !== "keep") || (newKey.trim() !== "" && newValue !== "");

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-auto bg-[var(--color-accent)]/40 p-6">
      <div className="w-full max-w-3xl rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-5 shadow-xl">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Edit configuration — {topic}</h2>
          <button onClick={onClose} className="text-[var(--color-text-subtle)] hover:text-[var(--color-text)]">
            ✕
          </button>
        </div>
        <div className="space-y-2">
          <div className="text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
            Current overrides
          </div>
          {rows.length === 0 ? (
            <div className="text-sm text-[var(--color-text-muted)]">No non-default overrides.</div>
          ) : (
            <table className="w-full text-sm">
              <tbody className="divide-y divide-[var(--color-border)]">
                {rows.map((r, i) => (
                  <tr key={r.name}>
                    <td className="py-1 pr-2 font-mono text-xs">{r.name}</td>
                    <td className="py-1 pr-2">
                      <input
                        type="text"
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
                        className="w-full rounded border border-[var(--color-border)] px-2 py-1 font-mono text-xs disabled:bg-[var(--color-surface-subtle)]"
                      />
                    </td>
                    <td className="py-1 pr-2 text-xs">
                      <select
                        value={r.op}
                        onChange={(e) =>
                          setRows((prev) => {
                            const next = [...prev];
                            next[i] = { ...next[i], op: e.target.value as "set" | "delete" | "keep" };
                            return next;
                          })
                        }
                        className="rounded border border-[var(--color-border)] px-1.5 py-0.5 text-xs"
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
          <div className="mt-4 text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
            Add / override key
          </div>
          <div className="flex gap-2">
            <input
              placeholder="key (e.g. retention.ms)"
              value={newKey}
              onChange={(e) => setNewKey(e.target.value)}
              className="flex-1 rounded border border-[var(--color-border)] px-2 py-1 font-mono text-xs"
            />
            <input
              placeholder="value"
              value={newValue}
              onChange={(e) => setNewValue(e.target.value)}
              className="flex-1 rounded border border-[var(--color-border)] px-2 py-1 font-mono text-xs"
            />
          </div>
        </div>

        {mut.error && (
          <div className="mt-3 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger-subtle)] px-3 py-2 text-xs text-[var(--color-danger)]">
            {(mut.error as Error).message}
          </div>
        )}
        {results && (
          <div className="mt-3">
            <div className="text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
              Results
            </div>
            <ul className="mt-1 space-y-0.5 text-sm">
              {results.map((r, i) => (
                <li key={i} className="flex items-center justify-between">
                  <span className="font-mono text-xs">
                    {r.op} {r.name}
                  </span>
                  {r.error ? (
                    <span className="text-xs text-[var(--color-danger)]">{r.error}</span>
                  ) : (
                    <span className="text-xs text-[var(--color-success)]">ok</span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}

        <div className="mt-5 flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-md border border-[var(--color-border-strong)] bg-[var(--color-surface-raised)] px-3 py-1.5 text-sm hover:border-[var(--color-border-strong)]"
          >
            Close
          </button>
          <button
            onClick={() => mut.mutate()}
            disabled={!hasChanges || mut.isPending}
            className="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm text-[var(--color-text-on-accent)] disabled:opacity-50"
          >
            {mut.isPending ? "Applying…" : "Apply"}
          </button>
        </div>
      </div>
    </div>
  );
}
