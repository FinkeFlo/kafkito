import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Download, Pencil, Plus, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/EmptyState";
import { Badge } from "@/components/badge";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Card } from "@/components/card";
import { Button } from "@/components/button";
import { Input } from "@/components/Input";
import { Modal } from "@/components/Modal";
import { Notice } from "@/components/Notice";
import {
  deletePrivateCluster,
  exportBundle,
  importBundle,
  listPrivateClusters,
  upsertPrivateCluster,
  type PrivateCluster,
  type PrivateClusterAuth,
} from "@/lib/private-clusters";
import { testCluster } from "@/lib/api";
import { useCluster } from "@/lib/use-cluster";

export const Route = createFileRoute("/settings/clusters")({
  validateSearch: (s: Record<string, unknown>) => ({
    cluster: typeof s.cluster === "string" ? s.cluster : undefined,
  }),
  component: ClusterSettingsPage,
});

interface FormState {
  id?: string;
  name: string;
  brokersCSV: string;
  tlsEnabled: boolean;
  tlsInsecure: boolean;
  authType: PrivateClusterAuth["type"];
  username: string;
  password: string;
  srURL: string;
  srUsername: string;
  srPassword: string;
  srInsecure: boolean;
}

const emptyForm: FormState = {
  name: "",
  brokersCSV: "",
  tlsEnabled: false,
  tlsInsecure: false,
  authType: "none",
  username: "",
  password: "",
  srURL: "",
  srUsername: "",
  srPassword: "",
  srInsecure: false,
};

function coldDNSHint(msg: string): string {
  const m = msg.toLowerCase();
  if (m.includes("i/o timeout") || m.includes("dial")) {
    return " — first probe is slow on cold broker DNS; cluster connections cache for ~15min after first contact, so a retry usually succeeds in <1s.";
  }
  return "";
}

function toPrivateCluster(f: FormState): Omit<PrivateCluster, "id" | "created_at" | "updated_at"> & { id?: string } {
  const brokers = f.brokersCSV
    .split(",")
    .map((b) => b.trim())
    .filter(Boolean);
  return {
    id: f.id,
    name: f.name.trim(),
    brokers,
    auth: {
      type: f.authType,
      username: f.authType === "none" ? undefined : f.username || undefined,
      password: f.authType === "none" ? undefined : f.password || undefined,
    },
    tls: { enabled: f.tlsEnabled, insecure_skip_verify: f.tlsInsecure },
    schema_registry: f.srURL
      ? {
          url: f.srURL,
          username: f.srUsername || undefined,
          password: f.srPassword || undefined,
          insecure_skip_verify: f.srInsecure,
        }
      : undefined,
  };
}

function fromPrivateCluster(c: PrivateCluster): FormState {
  return {
    id: c.id,
    name: c.name,
    brokersCSV: c.brokers.join(", "),
    tlsEnabled: !!c.tls?.enabled,
    tlsInsecure: !!c.tls?.insecure_skip_verify,
    authType: c.auth?.type ?? "none",
    username: c.auth?.username ?? "",
    password: c.auth?.password ?? "",
    srURL: c.schema_registry?.url ?? "",
    srUsername: c.schema_registry?.username ?? "",
    srPassword: c.schema_registry?.password ?? "",
    srInsecure: !!c.schema_registry?.insecure_skip_verify,
  };
}

function ClusterSettingsPage() {
  // Subscribe via the hook so list refreshes on any mutation.
  useCluster();
  const [tick, setTick] = useState(0);
  const items = useMemo(() => listPrivateClusters(), [tick]);
  const forceRefresh = () => setTick((n) => n + 1);

  const [form, setForm] = useState<FormState | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<PrivateCluster | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  // Drop ids for clusters that no longer exist (e.g. after delete or
  // import-replacing). Keeps the "select all" header checkbox honest.
  useEffect(() => {
    setSelected((prev) => {
      const live = new Set(items.map((c) => c.id));
      let changed = false;
      const next = new Set<string>();
      for (const id of prev) {
        if (live.has(id)) next.add(id);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [items]);

  const allSelected = items.length > 0 && items.every((c) => selected.has(c.id));
  const someSelected = !allSelected && items.some((c) => selected.has(c.id));

  const headerCheckboxRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (headerCheckboxRef.current) {
      headerCheckboxRef.current.indeterminate = someSelected;
    }
  }, [someSelected]);

  const toggleAll = () =>
    setSelected(allSelected ? new Set() : new Set(items.map((c) => c.id)));
  const toggleOne = (id: string) =>
    setSelected((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const openNew = () => setForm({ ...emptyForm });
  const openEdit = (c: PrivateCluster) => setForm(fromPrivateCluster(c));
  const closeForm = () => setForm(null);

  const onExport = () => {
    const bundle = exportBundle(selected.size > 0 ? selected : undefined);
    const blob = new Blob([JSON.stringify(bundle, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `kafkito-private-clusters-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const exportLabel =
    selected.size > 0 ? `Export ${selected.size} selected` : "Export JSON";

  const onImportFile = async (file: File) => {
    try {
      const text = await file.text();
      const res = importBundle(text);
      toast.success(
        `Imported: ${res.added} added, ${res.updated} updated, ${res.skipped} skipped`,
      );
      forceRefresh();
    } catch (e) {
      toast.error(`Import failed: ${(e as Error).message}`);
    }
  };

  return (
    <div className="space-y-5 p-6">
      <PageHeader
        title="Private clusters"
        subtitle="Cluster connections stored in your browser. Credentials stay on this device unless you export them. They are sent to the backend only when selected, per request."
        actions={
          <>
            <Button
              variant="primary"
              size="sm"
              leadingIcon={<Plus className="h-4 w-4" aria-hidden />}
              onClick={openNew}
            >
              Add cluster
            </Button>
            <Button
              variant="secondary"
              size="sm"
              leadingIcon={<Download className="h-4 w-4" aria-hidden />}
              onClick={onExport}
              disabled={items.length === 0}
            >
              {exportLabel}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              leadingIcon={<Upload className="h-4 w-4" aria-hidden />}
              onClick={() => fileRef.current?.click()}
            >
              Import JSON
            </Button>
            <input
              ref={fileRef}
              type="file"
              accept="application/json,.json"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void onImportFile(f);
                e.target.value = "";
              }}
            />
          </>
        }
      />

      <Card>
        {items.length === 0 ? (
          <EmptyState
            title="No private clusters yet"
            description="Add a cluster to connect with your own credentials. Shared clusters configured server-side always appear too."
          />
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase tracking-wide text-muted">
              <tr>
                <th className="w-10 px-4 py-2">
                  <input
                    ref={headerCheckboxRef}
                    type="checkbox"
                    checked={allSelected}
                    onChange={toggleAll}
                    aria-label={
                      allSelected
                        ? "Deselect all clusters"
                        : "Select all clusters for export"
                    }
                    className="h-4 w-4 cursor-pointer accent-accent"
                  />
                </th>
                <th className="px-4 py-2">Name</th>
                <th className="px-4 py-2">Brokers</th>
                <th className="px-4 py-2">Auth</th>
                <th className="px-4 py-2">TLS</th>
                <th className="px-4 py-2">SR</th>
                <th className="px-4 py-2 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map((c) => (
                <tr
                  key={c.id}
                  className="border-t border-border"
                >
                  <td className="px-4 py-2">
                    <input
                      type="checkbox"
                      checked={selected.has(c.id)}
                      onChange={() => toggleOne(c.id)}
                      aria-label={`Select ${c.name} for export`}
                      className="h-4 w-4 cursor-pointer accent-accent"
                    />
                  </td>
                  <td className="px-4 py-2 font-mono text-[13px] tabular-nums font-medium">{c.name}</td>
                  <td className="px-4 py-2 text-muted">
                    {c.brokers.join(", ")}
                  </td>
                  <td className="px-4 py-2">
                    <Badge variant="neutral">{c.auth.type}</Badge>
                  </td>
                  <td className="px-4 py-2">
                    {c.tls.enabled ? <Badge variant="neutral">on</Badge> : <span className="text-muted">off</span>}
                  </td>
                  <td className="px-4 py-2">
                    {c.schema_registry?.url ? (
                      <Badge variant="info">yes</Badge>
                    ) : (
                      <span className="text-muted">—</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <div className="inline-flex items-center gap-1">
                      <button
                        type="button"
                        onClick={() => openEdit(c)}
                        className="rounded-md p-1.5 text-muted hover:bg-hover hover:text-text"
                        aria-label="Edit"
                      >
                        <Pencil className="h-4 w-4" />
                      </button>
                      <button
                        type="button"
                        onClick={() => setConfirmDelete(c)}
                        className="rounded-md p-1.5 text-danger hover:bg-hover"
                        aria-label="Delete"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>

      {form && (
        <ClusterForm
          initial={form}
          onClose={closeForm}
          onSaved={() => {
            closeForm();
            forceRefresh();
          }}
        />
      )}

      <ConfirmDialog
        open={!!confirmDelete}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
        title="Delete private cluster?"
        description={`This removes "${confirmDelete?.name ?? ""}" from this browser. Credentials cannot be recovered unless you have an export.`}
        confirmLabel="Delete"
        variant="danger"
        onConfirm={() => {
          if (confirmDelete) {
            deletePrivateCluster(confirmDelete.id);
            toast.success("Cluster removed");
            forceRefresh();
          }
          setConfirmDelete(null);
        }}
      />
    </div>
  );
}

function ClusterForm({
  initial,
  onClose,
  onSaved,
}: {
  initial: FormState;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [f, setF] = useState<FormState>(initial);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<string | null>(null);
  const [testElapsed, setTestElapsed] = useState(0);

  useEffect(() => {
    if (!testing) {
      setTestElapsed(0);
      return;
    }
    const start = Date.now();
    const id = window.setInterval(() => {
      setTestElapsed(Math.floor((Date.now() - start) / 1000));
    }, 500);
    return () => window.clearInterval(id);
  }, [testing]);

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) =>
    setF((s) => ({ ...s, [k]: v }));

  const canSave =
    f.name.trim().length > 0 &&
    f.brokersCSV.trim().length > 0 &&
    (f.authType === "none" || (f.username && f.password));

  const onTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const draft = { ...toPrivateCluster(f), id: f.id ?? "__draft__", created_at: 0, updated_at: 0 } as PrivateCluster;
      const info = await testCluster(draft);
      if (info.reachable) {
        setTestResult(`OK — reachable (${info.auth_type}, TLS: ${info.tls ? "yes" : "no"})`);
      } else {
        const err = info.error ?? "unknown error";
        setTestResult(`Unreachable: ${err}${coldDNSHint(err)}`);
      }
    } catch (e) {
      const msg = (e as Error).message;
      setTestResult(`Error: ${msg}${coldDNSHint(msg)}`);
    } finally {
      setTesting(false);
    }
  };

  const onSave = () => {
    try {
      const saved = upsertPrivateCluster(toPrivateCluster(f));
      toast.success(`Saved "${saved.name}"`);
      onSaved();
    } catch (e) {
      toast.error(`Save failed: ${(e as Error).message}`);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={f.id ? "Edit private cluster" : "Add private cluster"}
      actions={
        <>
          <Button
            variant="secondary"
            size="sm"
            onClick={onTest}
            disabled={testing || !canSave}
          >
            {testing ? "Testing…" : "Test connection"}
          </Button>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={onSave}
            disabled={!canSave}
          >
            Save
          </Button>
        </>
      }
    >
      <p className="text-sm text-muted">
        Credentials are stored in this browser's localStorage in plaintext.
        Use Export/Import to migrate between devices.
      </p>

      <div className="mt-4 grid gap-4">
        <Field label="Name" required>
          <Input
            value={f.name}
            onChange={(e) => set("name", e.target.value)}
            placeholder="my-dev-cluster"
          />
        </Field>

        <Field label="Brokers (comma-separated)" required>
          <Input
            value={f.brokersCSV}
            onChange={(e) => set("brokersCSV", e.target.value)}
            placeholder="host1:9092, host2:9092"
          />
        </Field>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Auth type">
            <select
              className={selectCls}
              value={f.authType}
              onChange={(e) => set("authType", e.target.value as PrivateClusterAuth["type"])}
            >
              <option value="none">none</option>
              <option value="plain">SASL/PLAIN</option>
              <option value="scram-sha-256">SCRAM-SHA-256</option>
              <option value="scram-sha-512">SCRAM-SHA-512</option>
            </select>
          </Field>
          <Field label="TLS">
            <div className="flex items-center gap-3 pt-2">
              <label className="inline-flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={f.tlsEnabled}
                  onChange={(e) => set("tlsEnabled", e.target.checked)}
                />
                Enabled
              </label>
              <label className="inline-flex items-center gap-2 text-sm text-muted">
                <input
                  type="checkbox"
                  checked={f.tlsInsecure}
                  disabled={!f.tlsEnabled}
                  onChange={(e) => set("tlsInsecure", e.target.checked)}
                />
                Skip verify
              </label>
            </div>
          </Field>
        </div>

        {f.authType !== "none" && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Username" required>
              <Input
                value={f.username}
                onChange={(e) => set("username", e.target.value)}
                autoComplete="off"
              />
            </Field>
            <Field label="Password" required>
              <Input
                type="password"
                value={f.password}
                onChange={(e) => set("password", e.target.value)}
                autoComplete="new-password"
              />
            </Field>
          </div>
        )}

        <div className="rounded-xl border border-border p-3">
          <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted">
            Schema Registry (optional)
          </div>
          <Field label="URL">
            <Input
              value={f.srURL}
              onChange={(e) => set("srURL", e.target.value)}
              placeholder="https://schema-registry:8081"
            />
          </Field>
          {f.srURL && (
            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              <Field label="Username">
                <Input
                  value={f.srUsername}
                  onChange={(e) => set("srUsername", e.target.value)}
                  autoComplete="off"
                />
              </Field>
              <Field label="Password">
                <Input
                  type="password"
                  value={f.srPassword}
                  onChange={(e) => set("srPassword", e.target.value)}
                  autoComplete="new-password"
                />
              </Field>
              <label className="col-span-full inline-flex items-center gap-2 text-sm text-muted">
                <input
                  type="checkbox"
                  checked={f.srInsecure}
                  onChange={(e) => set("srInsecure", e.target.checked)}
                />
                Skip TLS verify
              </label>
            </div>
          )}
        </div>
      </div>

      {testing && testElapsed >= 3 && (
        <Notice intent="info" className="mt-4">
          Probing brokers… ~{testElapsed}s
        </Notice>
      )}
      {!testing && testResult && (
        <Notice
          intent={testResult.startsWith("OK") ? "success" : "danger"}
          className="mt-4"
        >
          {testResult}
        </Notice>
      )}
    </Modal>
  );
}

const selectCls =
  "h-9 w-full rounded-md border border-border bg-panel px-3 text-sm hover:border-border-hover";

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium uppercase tracking-wider text-muted">
        {label}
        {required && <span className="ml-1 text-danger">*</span>}
      </span>
      {children}
    </label>
  );
}
