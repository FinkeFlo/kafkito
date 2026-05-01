import { createFileRoute } from "@tanstack/react-router";
import { PageHeader } from "@/components/page-header";

export const Route = createFileRoute("/clusters/$cluster/schemas/$subject/")({
  component: SubjectOverview,
});

function SubjectOverview() {
  const { subject } = Route.useParams();
  const decoded = decodeURIComponent(subject);
  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={<span className="font-mono normal-case tracking-normal">Schema subject</span>}
        title={decoded}
        subtitle="Versions"
      />
      {/* TODO(frontend): version list + diff + compatibility */}
    </div>
  );
}
