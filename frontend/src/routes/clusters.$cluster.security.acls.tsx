import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { ChevronDown, Shield, Trash2 } from "lucide-react";
import {
  createACL,
  deleteACL,
  listACLs,
  type ACLEntry,
  type ACLSpec,
} from "@/lib/api";
import { Tag } from "@/components/Tag";
import { KpiCard } from "@/components/KpiCard";
import { DataTable, DataTableHead, DataTableRow, DataTableTh } from "@/components/DataTable";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SearchInput } from "@/components/search-input";
import { Highlight } from "@/components/highlight";
import { Toolbar } from "@/components/Toolbar";
import { Button } from "@/components/button";
import { Modal } from "@/components/Modal";
import { Input } from "@/components/Input";
import { Notice } from "@/components/Notice";
import { useFuzzy } from "@/lib/fuzzy";

export const Route = createFileRoute("/clusters/$cluster/security/acls")({
  component: ACLsPage,
});

const RESOURCE_TYPES = ["TOPIC", "GROUP", "CLUSTER", "TRANSACTIONAL_ID", "DELEGATION_TOKEN"];
const PATTERN_TYPES = ["LITERAL", "PREFIXED"];
const OPERATIONS = [
  "ALL",
  "READ",
  "WRITE",
  "CREATE",
  "DELETE",
  "ALTER",
  "DESCRIBE",
  "CLUSTER_ACTION",
  "DESCRIBE_CONFIGS",
  "ALTER_CONFIGS",
  "IDEMPOTENT_WRITE",
];
const PERMISSIONS = ["ALLOW", "DENY"];

function ACLsPage() {
  const { cluster } = Route.useParams();
  return <ACLsBody cluster={cluster} />;
}

function ACLsBody({ cluster }: { cluster: string }) {
  const qc = useQueryClient();
  const [filter, setFilter] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [pending, setPending] = useState<ACLEntry | null>(null);
  const [banner, setBanner] = useState<{ kind: "ok" | "err"; msg: string } | null>(null);

  const q = useQuery({
    queryKey: ["acls", cluster],
    queryFn: () => listACLs(cluster),
  });

  const delMut = useMutation({
    mutationFn: (spec: ACLSpec) => deleteACL(cluster, spec),
    onSuccess: (n, spec) => {
      setBanner({
        kind: "ok",
        msg: `${n} ACL(s) deleted (${spec.principal} → ${spec.operation} on ${spec.resource_name})`,
      });
      setPending(null);
      qc.invalidateQueries({ queryKey: ["acls", cluster] });
    },
    onError: (e: Error) => setBanner({ kind: "err", msg: e.message }),
  });

  const rows = q.data ?? [];
  const fuzzy = useFuzzy(rows, {
    keys: ["principal", "resource_name", "resource_type", "operation"],
    query: filter,
  });
  const filtered = fuzzy.results;

  const stats = useMemo(() => {
    const principals = new Set(rows.map((r) => r.principal)).size;
    const allow = rows.filter((r) => r.permission_type === "ALLOW").length;
    const deny = rows.filter((r) => r.permission_type === "DENY").length;
    return { principals, allow, deny };
  }, [rows]);

  const errorMessage = (q.error as Error | undefined)?.message;
  // Distinguish "permission missing" (degraded capability) from real fetch
  // failures. Brokers reject DESCRIBE on the cluster-level ACL listing with
  // CLUSTER_AUTHORIZATION_FAILED / NOT_AUTHORIZED / "not authorized" in the
  // message body. Anything else (timeout, 5xx, network) keeps the red
  // ErrorState path so the user is prompted to retry.
  const isPermissionDenied =
    !!errorMessage &&
    /authoriz|not\s+authorized|permission|forbidden|describe/i.test(errorMessage);
  const limited = q.isError && isPermissionDenied;
  const aclsCapDisabledId = "acls-capability-disabled-reason";

  return (
    <>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard label="Principals" value={stats.principals} />
        <KpiCard label="Allow rules" value={stats.allow} />
        <KpiCard label="Deny rules" value={stats.deny} />
        {/* TODO(backend): ACL usage / last_seen_at per rule to drive "Unused (90d)" */}
        <KpiCard label="Unused (90d)" value="—" delta="pending usage data" />
      </div>

      {limited && (
        <Notice
          intent="warning"
          title="Limited capability"
        >
          <span id={aclsCapDisabledId} className="block">
            The configured Kafka user lacks{" "}
            <code className="font-mono">DESCRIBE</code> on{" "}
            <code className="font-mono">CLUSTER:*</code>, so ACL rules cannot
            be listed on this cluster. Granting it will enable this view.
            {errorMessage ? (
              <span className="mt-1 block text-xs text-muted">
                Broker response:{" "}
                <span className="font-mono">{errorMessage}</span>
              </span>
            ) : null}
          </span>
        </Notice>
      )}

      <Toolbar
        search={
          <SearchInput
            value={filter}
            onChange={setFilter}
            placeholder="Filter by principal, resource, or operation…"
            ariaLabel="Filter ACLs"
            count={{ visible: filtered.length, total: rows.length }}
          />
        }
        filters={
          <>
            <PlaceholderFilter label="Principal: all" />
            <PlaceholderFilter label="Resource: all" />
            <PlaceholderFilter label="Permission: all" />
          </>
        }
        actions={
          <Button
            variant="primary"
            size="sm"
            onClick={() => setShowCreate(true)}
            disabled={limited}
            aria-describedby={limited ? aclsCapDisabledId : undefined}
          >
            + New rule
          </Button>
        }
      />

      {banner && (
        <Notice intent={banner.kind === "ok" ? "success" : "danger"}>
          {banner.msg}{" "}
          <button
            type="button"
            className="ml-2 underline"
            onClick={() => setBanner(null)}
          >
            dismiss
          </button>
        </Notice>
      )}

      {q.isError && !limited && (
        <ErrorState
          title="Failed to load ACLs"
          detail={errorMessage}
          onRetry={() => q.refetch()}
        />
      )}

      {!q.isError && (
        <>
          {q.isLoading ? (
            <TableSkeleton />
          ) : filtered.length === 0 ? (
            <EmptyState
              icon={Shield}
              title={filter ? "No ACL matches your filter" : "No ACL rules"}
              description={
                filter ? undefined : "Create your first rule to grant access to principals."
              }
            />
          ) : (
            <DataTable>
              <DataTableHead>
                <tr>
                  <DataTableTh>Principal</DataTableTh>
                  <DataTableTh>Resource</DataTableTh>
                  <DataTableTh>Operation</DataTableTh>
                  <DataTableTh>Permission</DataTableTh>
                  <DataTableTh>Host</DataTableTh>
                  <DataTableTh className="w-12" />
                </tr>
              </DataTableHead>
              <tbody>
                {filtered.map((r, i) => (
                  <DataTableRow key={`${r.principal}|${r.resource_type}:${r.resource_name}|${r.operation}|${i}`}>
                    <td className="px-4 py-2.5 font-mono text-[13px] tabular-nums">
                      <Highlight text={r.principal} ranges={fuzzy.rangesFor(r, "principal")} />
                    </td>
                    <td className="px-4 py-2.5">
                      <div className="flex items-center gap-1.5">
                        <Tag>
                          <Highlight text={r.resource_type} ranges={fuzzy.rangesFor(r, "resource_type")} />
                        </Tag>
                        <span className="font-mono text-[13px] tabular-nums">
                          <Highlight text={r.resource_name} ranges={fuzzy.rangesFor(r, "resource_name")} />
                        </span>
                        {r.pattern_type !== "LITERAL" && (
                          <span className="text-[10px] text-muted">({r.pattern_type})</span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-[13px] tabular-nums text-muted">
                      <Highlight text={r.operation} ranges={fuzzy.rangesFor(r, "operation")} />
                    </td>
                    <td className="px-4 py-2.5">
                      <Tag variant={r.permission_type === "ALLOW" ? "success" : "danger"}>
                        {r.permission_type}
                      </Tag>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-[13px] tabular-nums text-muted">{r.host}</td>
                    <td className="px-4 py-2.5 text-right">
                      <button
                        type="button"
                        onClick={() => setPending(r)}
                        className="text-subtle-text transition-colors hover:text-danger"
                        aria-label="Delete ACL"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </td>
                  </DataTableRow>
                ))}
              </tbody>
            </DataTable>
          )}
        </>
      )}

      {showCreate && (
        <CreateACLModal
          cluster={cluster}
          onClose={() => setShowCreate(false)}
          onDone={(msg) => {
            setBanner({ kind: "ok", msg });
            setShowCreate(false);
            qc.invalidateQueries({ queryKey: ["acls", cluster] });
          }}
          onError={(msg) => setBanner({ kind: "err", msg })}
        />
      )}

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(v) => !v && setPending(null)}
        title="Delete this ACL?"
        description={
          pending ? (
            <span className="block">
              <span className="block">
                {pending.principal} · {pending.permission_type} {pending.operation}
              </span>
              <span className="block font-mono text-[13px] tabular-nums">
                {pending.resource_type}:{pending.resource_name} ({pending.pattern_type})
              </span>
            </span>
          ) : undefined
        }
        confirmLabel="Delete ACL"
        variant="danger"
        onConfirm={() => {
          if (pending) delMut.mutate(pending);
        }}
      />
    </>
  );
}

function PlaceholderFilter({ label }: { label: string }) {
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

function TableSkeleton() {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-panel">
      {[0, 1, 2, 3, 4].map((i) => (
        <div key={i} className="flex items-center gap-4 border-t border-border px-4 py-3 first:border-t-0">
          <div className="h-3 w-48 animate-pulse rounded bg-subtle" />
          <div className="h-3 w-24 animate-pulse rounded bg-subtle" />
          <div className="ml-auto h-3 w-16 animate-pulse rounded bg-subtle" />
        </div>
      ))}
    </div>
  );
}

function CreateACLModal({
  cluster,
  onClose,
  onDone,
  onError,
}: {
  cluster: string;
  onClose: () => void;
  onDone: (msg: string) => void;
  onError: (msg: string) => void;
}) {
  const [spec, setSpec] = useState<ACLSpec>({
    principal: "User:",
    host: "*",
    resource_type: "TOPIC",
    resource_name: "",
    pattern_type: "LITERAL",
    operation: "READ",
    permission_type: "ALLOW",
  });
  const mut = useMutation({
    mutationFn: () => createACL(cluster, spec),
    onSuccess: () =>
      onDone(
        `ACL created: ${spec.principal} ${spec.permission_type} ${spec.operation} on ${spec.resource_type}:${spec.resource_name}`,
      ),
    onError: (e: Error) => onError(e.message),
  });
  const update = (k: keyof ACLSpec, v: string) => setSpec({ ...spec, [k]: v });

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={
        <span className="flex flex-col">
          <span className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            New ACL on
          </span>
          <span className="font-mono text-[13px] font-semibold">{cluster}</span>
        </span>
      }
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => mut.mutate()}
            disabled={mut.isPending || !spec.principal.trim() || !spec.resource_name.trim()}
          >
            {mut.isPending ? "Creating…" : "Create ACL"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <Field label="Principal" hint="e.g. User:checkout-team">
          <Input
            value={spec.principal}
            onChange={(e) => update("principal", e.target.value)}
            className="font-mono text-xs"
          />
        </Field>
        <Field label="Host" hint="* for all">
          <Input
            value={spec.host}
            onChange={(e) => update("host", e.target.value)}
            className="font-mono text-xs"
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Resource type">
            <Select
              value={spec.resource_type}
              options={RESOURCE_TYPES}
              onChange={(v) => update("resource_type", v)}
            />
          </Field>
          <Field label="Pattern">
            <Select
              value={spec.pattern_type}
              options={PATTERN_TYPES}
              onChange={(v) => update("pattern_type", v)}
            />
          </Field>
        </div>
        <Field
          label="Resource name"
          hint={spec.resource_type === "CLUSTER" ? "use kafka-cluster" : undefined}
        >
          <Input
            value={spec.resource_name}
            onChange={(e) => update("resource_name", e.target.value)}
            placeholder={spec.resource_type === "CLUSTER" ? "kafka-cluster" : "my-topic"}
            className="font-mono text-xs"
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Operation">
            <Select
              value={spec.operation}
              options={OPERATIONS}
              onChange={(v) => update("operation", v)}
            />
          </Field>
          <Field label="Permission">
            <Select
              value={spec.permission_type}
              options={PERMISSIONS}
              onChange={(v) => update("permission_type", v)}
            />
          </Field>
        </div>
      </div>
    </Modal>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label className="block text-[11px] font-semibold uppercase tracking-wider text-muted">
        {label}
      </label>
      <div className="mt-1">{children}</div>
      {hint && <div className="mt-0.5 text-xs text-subtle-text">{hint}</div>}
    </div>
  );
}

function Select({
  value,
  options,
  onChange,
}: {
  value: string;
  options: string[];
  onChange: (v: string) => void;
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="h-9 w-full rounded-md border border-border bg-panel px-2 text-xs text-text hover:border-border-hover"
    >
      {options.map((v) => (
        <option key={v} value={v}>
          {v}
        </option>
      ))}
    </select>
  );
}
