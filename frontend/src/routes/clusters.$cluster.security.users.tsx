import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { KeyRound, Trash2 } from "lucide-react";
import {
  deleteSCRAMUser,
  listSCRAMUsers,
  upsertSCRAMUser,
  type SCRAMMechanism,
  type SCRAMUser,
} from "@/lib/api";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import { Badge } from "@/components/badge";
import { EmptyState } from "@/components/EmptyState";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/button";
import { Input } from "@/components/Input";
import { Modal } from "@/components/Modal";
import { Notice } from "@/components/Notice";

export const Route = createFileRoute("/clusters/$cluster/security/users")({
  component: UsersPage,
});

const MECHANISMS: readonly SCRAMMechanism[] = ["SCRAM-SHA-256", "SCRAM-SHA-512"];

function toMechanism(m: string): SCRAMMechanism {
  return MECHANISMS.find((x) => x === m) ?? "SCRAM-SHA-256";
}

type Row = { user: string; mechanism: string; iterations: number };

function UsersPage() {
  const { cluster } = Route.useParams();
  return <UsersBody cluster={cluster} />;
}

function UsersBody({ cluster }: { cluster: string }) {
  const qc = useQueryClient();
  const [showUpsert, setShowUpsert] = useState(false);
  const [upsertDefaults, setUpsertDefaults] = useState<{ user: string; mechanism: string }>({
    user: "",
    mechanism: "SCRAM-SHA-256",
  });
  const [pendingDelete, setPendingDelete] = useState<{ user: string; mechanism: string } | null>(
    null,
  );
  const [banner, setBanner] = useState<{ kind: "ok" | "err"; msg: string } | null>(null);

  const q = useQuery({
    queryKey: ["scram-users", cluster],
    queryFn: () => listSCRAMUsers(cluster),
  });

  const delMut = useMutation({
    mutationFn: (p: { user: string; mechanism: string }) =>
      deleteSCRAMUser(cluster, p.user, p.mechanism),
    onSuccess: (_, p) => {
      setBanner({ kind: "ok", msg: `Credential deleted: ${p.user} / ${p.mechanism}` });
      setPendingDelete(null);
      qc.invalidateQueries({ queryKey: ["scram-users", cluster] });
    },
    onError: (e: Error) => setBanner({ kind: "err", msg: e.message }),
  });

  const users = q.data ?? [];
  const rows = useMemo<Row[]>(() => {
    return users.flatMap((u) =>
      u.credentials.length === 0
        ? [{ user: u.user, mechanism: "—", iterations: 0 }]
        : u.credentials.map((c) => ({
            user: u.user,
            mechanism: c.mechanism,
            iterations: c.iterations,
          })),
    );
  }, [users]);

  const columns = useMemo<DataTableColumn<Row>[]>(
    () => [
      {
        id: "user",
        header: "User",
        sortValue: (r) => r.user,
        cell: (r) => <span className="font-mono text-[13px] tabular-nums">{r.user}</span>,
      },
      {
        id: "mechanism",
        header: "Mechanism",
        className: "w-48",
        sortValue: (r) => r.mechanism,
        cell: (r) =>
          r.mechanism === "—" ? (
            <span className="text-muted">—</span>
          ) : (
            <Badge variant="neutral">{r.mechanism}</Badge>
          ),
      },
      {
        id: "iterations",
        header: "Iterations",
        className: "w-32",
        align: "right",
        sortValue: (r) => r.iterations,
        cell: (r) => (r.iterations ? r.iterations : <span className="text-muted">—</span>),
      },
      {
        id: "actions",
        header: "",
        className: "w-40",
        align: "right",
        cell: (r) =>
          r.mechanism === "—" ? null : (
            <div className="flex items-center justify-end gap-2">
              <Button
                variant="secondary"
                size="sm"
                onClick={(e) => {
                  e.stopPropagation();
                  setUpsertDefaults({ user: r.user, mechanism: r.mechanism });
                  setShowUpsert(true);
                }}
              >
                Rotate password
              </Button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  setPendingDelete({ user: r.user, mechanism: r.mechanism });
                }}
                aria-label="Delete"
                className="text-subtle-text transition-colors hover:text-danger"
              >
                <Trash2 className="h-4 w-4" />
              </button>
            </div>
          ),
      },
    ],
    [],
  );

  const empty = (
    <EmptyState
      icon={<KeyRound className="h-5 w-5" />}
      title="No SCRAM users"
      description="Create a SCRAM user to enable SASL authentication."
    />
  );

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Button
          variant="primary"
          size="sm"
          className="ml-auto"
          onClick={() => {
            setUpsertDefaults({ user: "", mechanism: "SCRAM-SHA-256" });
            setShowUpsert(true);
          }}
        >
          + New User
        </Button>
      </div>
      {banner && (
        <Notice intent={banner.kind === "ok" ? "success" : "danger"}>
          {banner.msg}{" "}
          <button type="button" className="ml-2 underline" onClick={() => setBanner(null)}>
            dismiss
          </button>
        </Notice>
      )}
      {q.error && <Notice intent="danger">{(q.error as Error).message}</Notice>}
      <DataTable<Row>
        columns={columns}
        rows={rows}
        rowKey={(r) => `${r.user}|${r.mechanism}`}
        isLoading={q.isLoading}
        emptyState={empty}
      />

      {showUpsert && (
        <UpsertModal
          cluster={cluster}
          defaultUser={upsertDefaults.user}
          defaultMechanism={upsertDefaults.mechanism}
          onClose={() => setShowUpsert(false)}
          onDone={(msg) => {
            setBanner({ kind: "ok", msg });
            setShowUpsert(false);
            qc.invalidateQueries({ queryKey: ["scram-users", cluster] });
          }}
          onError={(msg) => setBanner({ kind: "err", msg })}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(v) => !v && setPendingDelete(null)}
        title="Delete SCRAM credential?"
        description={
          pendingDelete ? (
            <span className="block space-y-1">
              <span className="block font-mono text-[13px] tabular-nums">
                {pendingDelete.user} / {pendingDelete.mechanism}
              </span>
              <span className="block">
                The user will no longer be able to authenticate with this mechanism. Other
                mechanisms remain.
              </span>
            </span>
          ) : undefined
        }
        confirmLabel="Delete credential"
        variant="danger"
        onConfirm={() => {
          if (pendingDelete) delMut.mutate(pendingDelete);
        }}
      />
    </div>
  );
}

function UpsertModal({
  cluster,
  defaultUser,
  defaultMechanism,
  onClose,
  onDone,
  onError,
}: {
  cluster: string;
  defaultUser: string;
  defaultMechanism: string;
  onClose: () => void;
  onDone: (msg: string) => void;
  onError: (msg: string) => void;
}) {
  const [user, setUser] = useState(defaultUser);
  const [mechanism, setMechanism] = useState<SCRAMMechanism>(toMechanism(defaultMechanism));
  const [password, setPassword] = useState("");
  const [iterations, setIterations] = useState(8192);

  const mut = useMutation({
    mutationFn: () => upsertSCRAMUser(cluster, { user, mechanism, password, iterations }),
    onSuccess: () => onDone(`SCRAM credential set: ${user} / ${mechanism} (${iterations} it.)`),
    onError: (e: Error) => onError(e.message),
  });

  const rotating = defaultUser !== "";

  return (
    <Modal
      open
      onClose={onClose}
      size="md"
      title={rotating ? "Rotate password" : "Create / Update SCRAM User"}
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => mut.mutate()}
            disabled={
              mut.isPending || !user.trim() || !password || iterations < 4096 || iterations > 16384
            }
          >
            {mut.isPending ? "Saving…" : rotating ? "Rotate" : "Create"}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-sm">
        <div>
          <label className="block text-xs font-medium text-muted">User</label>
          <Input
            value={user}
            onChange={(e) => setUser(e.target.value)}
            readOnly={rotating}
            className={"mt-1 font-mono " + (rotating ? "bg-subtle text-muted" : "")}
            placeholder="alice"
          />
        </div>
        <div>
          <label className="block text-xs font-medium text-muted">Mechanism</label>
          <select
            value={mechanism}
            onChange={(e) => setMechanism(toMechanism(e.target.value))}
            disabled={rotating}
            className="mt-1 h-9 w-full rounded-md border border-border bg-panel px-3 text-sm hover:border-border-hover disabled:bg-subtle disabled:text-muted"
          >
            {MECHANISMS.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="block text-xs font-medium text-muted">Password</label>
          <Input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="mt-1 font-mono"
            placeholder="at least 1 character"
            autoFocus
          />
        </div>
        <div>
          <label className="block text-xs font-medium text-muted">
            Iterations <span className="text-subtle-text">(4096–16384, default 8192)</span>
          </label>
          <Input
            type="number"
            min={4096}
            max={16384}
            value={iterations}
            onChange={(e) => setIterations(Number(e.target.value) || 0)}
            className="mt-1 w-32"
          />
        </div>
      </div>
    </Modal>
  );
}

export type { SCRAMUser };
