import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { FileJson } from "lucide-react";
import { getSchemaVersion, listSubjects } from "@/lib/api";
import { EmptyState } from "@/components/EmptyState";
import { Tag } from "@/components/Tag";
import { Button } from "@/components/button";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/schema")({
  component: SchemaTab,
});

function SchemaTab() {
  const { cluster, topic } = Route.useParams();
  const subject = `${topic}-value`;

  const subjectsQuery = useQuery({
    queryKey: ["subjects", cluster],
    queryFn: () => listSubjects(cluster),
    enabled: !!cluster,
    staleTime: 60_000,
  });

  const hasSubject = subjectsQuery.data?.some((s) => s.name === subject) ?? false;

  const versionQuery = useQuery({
    queryKey: ["schema-version", cluster, subject, "latest"],
    queryFn: () => getSchemaVersion(cluster, subject, "latest"),
    enabled: !!cluster && hasSubject,
    staleTime: 60_000,
  });

  if (subjectsQuery.isLoading) {
    return (
      <div className="rounded-xl border border-border bg-panel p-6 text-sm text-muted">
        Loading schemas…
      </div>
    );
  }

  if (!hasSubject) {
    return (
      <EmptyState
        icon={FileJson}
        title="No schema registered for this topic"
        description={
          <>
            Register <span className="font-mono">{subject}</span> with the schema
            registry to get inline validation on produce.
          </>
        }
        action={
          <Link
            to="/clusters/$cluster/schemas/$subject"
            params={{ cluster, subject }}
            search={{ subject: undefined, version: undefined }}
          >
            <Button variant="primary" size="sm">
              Open in Schemas
            </Button>
          </Link>
        }
      />
    );
  }

  return (
    <div className="rounded-xl border border-border bg-panel">
      <div className="flex items-center justify-between border-b border-border p-4">
        <div>
          <div className="font-mono text-[13px] font-semibold">{subject}</div>
          <div className="mt-0.5 flex items-center gap-2 text-[11px] text-muted">
            {versionQuery.data?.schemaType && (
              <Tag variant="info">{versionQuery.data.schemaType}</Tag>
            )}
            <span>
              {versionQuery.data
                ? `v${versionQuery.data.version} · id ${versionQuery.data.id}`
                : "Version: latest"}
            </span>
            {versionQuery.data?.config?.compatibilityLevel && (
              <span>· {versionQuery.data.config.compatibilityLevel}</span>
            )}
          </div>
        </div>
        <Link
          to="/clusters/$cluster/schemas/$subject"
          params={{ cluster, subject }}
          search={{ subject: undefined, version: undefined }}
          className="rounded-md border border-border bg-panel px-2.5 py-1 text-xs text-text transition-colors hover:bg-hover"
        >
          Open full view
        </Link>
      </div>
      <pre className="overflow-auto p-4 font-mono text-[12px] leading-relaxed text-text">
        {versionQuery.isLoading ? "Loading…" : (versionQuery.data?.schema ?? "(empty)")}
      </pre>
    </div>
  );
}
