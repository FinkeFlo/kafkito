import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { FileJson, Trash2 } from "lucide-react";
import { clsx } from "clsx";
import {
  listSubjects,
  getSchemaVersion,
  deleteSubject,
  type Subject,
} from "@/lib/api";
import { useCluster } from "@/lib/use-cluster";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Tag } from "@/components/Tag";
import { EmptyState } from "@/components/EmptyState";
import { SearchInput } from "@/components/search-input";
import { Highlight } from "@/components/highlight";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/button";
import { Notice } from "@/components/Notice";
import { useFuzzy, type HighlightRange } from "@/lib/fuzzy";

export const Route = createFileRoute("/schemas")({
  validateSearch: (s: Record<string, unknown>) => ({
    cluster: typeof s.cluster === "string" ? s.cluster : undefined,
    subject: typeof s.subject === "string" ? s.subject : undefined,
    version: typeof s.version === "string" ? s.version : undefined,
  }),
  component: SchemasPage,
});

function SchemasPage() {
  const { subject, version } = Route.useSearch();
  const navigate = Route.useNavigate();
  const { cluster, clusters } = useCluster();
  const active = useMemo(
    () => clusters?.find((c) => c.name === cluster),
    [cluster, clusters],
  );

  return (
    <div className="space-y-5 p-6">
      <Header cluster={cluster} hasSR={!!active?.schema_registry} />

      {active && !active.schema_registry && (
        <Notice intent="warning" title="Schema Registry is not configured">
          Cluster <span className="font-mono">{active.name}</span> has no
          schema registry URL. Add one in Settings to browse subjects.
        </Notice>
      )}

      {active?.schema_registry && cluster && (
        <SchemasBody
          cluster={active.name}
          subject={subject}
          version={version}
          onSelect={(s, v) =>
            navigate({ search: { cluster: cluster, subject: s, version: v } })
          }
        />
      )}
    </div>
  );
}

function Header({ cluster, hasSR }: { cluster: string | null; hasSR: boolean }) {
  // Disabled "+ Register schema" carries load-bearing info — surface it via
  // `aria-describedby` instead of hover-only `title`. The button stays
  // disabled until the backend register-schema endpoint exists, so no
  // onClick handler is wired (it would be unreachable).
  const reasonId = "schemas-register-disabled-reason";
  return (
    <PageHeader
      eyebrow={
        <>
          <span className="font-mono normal-case tracking-normal">{cluster ?? "—"}</span>{" "}
          <span aria-hidden>›</span> Schema Registry
        </>
      }
      title="Schemas"
      subtitle={
        hasSR
          ? "Browse, inspect, and evolve subjects registered with this cluster's Schema Registry."
          : "Connect a Schema Registry to browse subjects."
      }
      actions={
        <>
          <Button
            variant="primary"
            size="sm"
            disabled
            aria-describedby={reasonId}
          >
            + Register schema
          </Button>
          <span id={reasonId} className="sr-only">
            Register-schema flow coming soon.
          </span>
        </>
      }
    />
  );
}

function SchemasBody({
  cluster,
  subject,
  version,
  onSelect,
}: {
  cluster: string;
  subject?: string;
  version?: string;
  onSelect: (s?: string, v?: string) => void;
}) {
  const [filter, setFilter] = useState("");
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const qc = useQueryClient();
  const subjectsQuery = useQuery({
    queryKey: ["schemas", cluster],
    queryFn: () => listSubjects(cluster),
  });
  const subjects = subjectsQuery.data ?? [];
  const sortedSubjects = useMemo(
    () => [...subjects].sort((a, b) => a.name.localeCompare(b.name)),
    [subjects],
  );
  const fuzzy = useFuzzy(sortedSubjects, { keys: ["name"], query: filter });
  const filtered = fuzzy.results;

  const deleteMut = useMutation({
    mutationFn: (name: string) => deleteSubject(cluster, name, false),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["schemas", cluster] });
      onSelect(undefined, undefined);
    },
  });

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[420px_1fr]">
      <div className="overflow-hidden rounded-xl border border-border bg-panel">
        <div className="border-b border-border p-2">
          <SearchInput
            value={filter}
            onChange={setFilter}
            placeholder="Filter subjects…"
            ariaLabel="Filter subjects"
            count={{ visible: filtered.length, total: subjects.length }}
            className="min-w-0"
          />
        </div>
        {subjectsQuery.isLoading && (
          <div className="p-4 text-sm text-muted">Loading subjects…</div>
        )}
        {subjectsQuery.error && (
          <div className="m-3">
            <Notice intent="danger">
              {(subjectsQuery.error as Error).message}
            </Notice>
          </div>
        )}
        {!subjectsQuery.isLoading && filtered.length === 0 && !subjectsQuery.error && (
          <div className="p-4 text-sm text-muted">No subjects match your filter.</div>
        )}
        <ul className="max-h-[65vh] overflow-auto">
          {filtered.map((s) => (
            <SubjectRow
              key={s.name}
              subject={s}
              active={s.name === subject}
              nameHighlight={fuzzy.rangesFor(s, "name")}
              onSelect={(v) => onSelect(s.name, v)}
              onDelete={() => setPendingDelete(s.name)}
            />
          ))}
        </ul>
      </div>

      <div>
        {subject ? (
          <SchemaDetail
            cluster={cluster}
            subject={subject}
            version={version ?? "latest"}
          />
        ) : (
          <EmptyState
            icon={FileJson}
            title="No subject selected"
            description="Pick a subject from the list on the left to view its schema."
          />
        )}
      </div>

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(v) => !v && setPendingDelete(null)}
        title={`Delete subject "${pendingDelete ?? ""}"?`}
        description="This soft-deletes every version of the subject. You can restore it from the registry if needed."
        confirmPhrase={pendingDelete ?? undefined}
        confirmLabel="Delete subject"
        variant="danger"
        onConfirm={() => {
          if (pendingDelete) deleteMut.mutate(pendingDelete);
          setPendingDelete(null);
        }}
      />
    </div>
  );
}

function SubjectRow({
  subject,
  active,
  onSelect,
  onDelete,
  nameHighlight,
}: {
  subject: Subject;
  active: boolean;
  onSelect: (version?: string) => void;
  onDelete: () => void;
  nameHighlight?: readonly HighlightRange[];
}) {
  const latest = subject.versions[subject.versions.length - 1];
  const type = subject.latest_schema_type || inferType(subject.name);
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect("latest")}
        className={clsx(
          "group flex w-full items-start justify-between gap-3 border-b border-border px-3 py-2.5 text-left transition-colors",
          active ? "bg-accent-subtle" : "hover:bg-hover",
        )}
      >
        <div className="min-w-0 flex-1">
          <div
            className={clsx(
              "truncate font-mono text-[13px] tabular-nums",
              active ? "font-semibold text-text" : "text-text",
            )}
            title={subject.name}
          >
            <Highlight text={subject.name} ranges={nameHighlight ?? []} />
          </div>
          <div className="mt-0.5 flex items-center gap-2 text-[11px] text-muted">
            <Tag>{type}</Tag>
            <span>v{latest}</span>
            <span>·</span>
            <span>{subject.versions.length} versions</span>
          </div>
        </div>
        <span
          role="button"
          tabIndex={0}
          onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              e.stopPropagation();
              onDelete();
            }
          }}
          aria-label={`Delete subject ${subject.name}`}
          className="mt-0.5 text-subtle-text opacity-0 transition-opacity hover:text-danger group-hover:opacity-100 focus-visible:opacity-100"
        >
          <Trash2 className="h-4 w-4" />
        </span>
      </button>
    </li>
  );
}

function inferType(subjectName: string): string {
  if (subjectName.endsWith("-value") || subjectName.endsWith("-key")) return "AVRO";
  return "AVRO";
}

function SchemaDetail({
  cluster,
  subject,
  version,
}: {
  cluster: string;
  subject: string;
  version: string;
}) {
  const versionQuery = useQuery({
    queryKey: ["schema", cluster, subject, version],
    queryFn: () => getSchemaVersion(cluster, subject, version),
  });
  const s = versionQuery.data;
  const prettySchema = useMemo(() => {
    if (!s) return "";
    try {
      return JSON.stringify(JSON.parse(s.schema), null, 2);
    } catch {
      return s.schema;
    }
  }, [s]);

  if (versionQuery.isLoading) {
    return (
      <div className="rounded-xl border border-border bg-panel p-6 text-sm text-muted">
        Loading schema…
      </div>
    );
  }
  if (versionQuery.error) {
    return (
      <Notice intent="danger">{(versionQuery.error as Error).message}</Notice>
    );
  }
  if (!s) return null;

  const tabs = ["Schema", `Versions (${s.version})`, "Compatibility", "References"];

  return (
    <div className="rounded-xl border border-border bg-panel">
      <div className="border-b border-border p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="truncate font-mono text-base font-semibold">{s.subject}</div>
            <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-muted">
              <span>Latest: v{s.version}</span>
              {s.config?.compatibilityLevel && (
                <>
                  <span>·</span>
                  <span>
                    Compatibility:{" "}
                    <span className="font-semibold text-text">
                      {s.config.compatibilityLevel}
                    </span>
                  </span>
                </>
              )}
              <span>·</span>
              <span>{s.references?.length ?? 0} references</span>
            </div>
          </div>
          <div className="flex gap-2">
            {["Compare", "History", "Evolve"].map((label) => (
              <button
                key={label}
                type="button"
                disabled
                aria-label={`${label} (coming soon)`}
                className="rounded-md border border-border bg-panel px-2.5 py-1 text-xs text-muted"
              >
                {label}
              </button>
            ))}
          </div>
        </div>
      </div>
      <div className="flex items-center gap-1 border-b border-border px-3">
        {tabs.map((tab, i) => (
          <span
            key={tab}
            className={clsx(
              "relative px-3 py-2 text-sm",
              i === 0
                ? "font-semibold text-text after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-accent"
                : "font-medium text-muted",
            )}
          >
            {tab}
          </span>
        ))}
      </div>

      {s.references && s.references.length > 0 && (
        <div className="border-b border-border px-4 py-3">
          <div className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            References
          </div>
          <ul className="mt-1 space-y-0.5">
            {s.references.map((r, i) => (
              <li key={i} className="font-mono text-[12px]">
                {r.name} → {r.subject}/v{r.version}
              </li>
            ))}
          </ul>
        </div>
      )}

      <pre className="max-h-[60vh] overflow-auto p-4 font-mono text-[12px] leading-relaxed text-text">
        {prettySchema}
      </pre>
    </div>
  );
}
