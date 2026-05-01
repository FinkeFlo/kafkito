import { createFileRoute } from "@tanstack/react-router";
import { PageHeader } from "@/components/page-header";

export const Route = createFileRoute("/clusters/$cluster/groups/$group/")({
  component: GroupOverview,
});

function GroupOverview() {
  const { group } = Route.useParams();
  const decoded = decodeURIComponent(group);
  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={<span className="font-mono normal-case tracking-normal">Consumer group</span>}
        title={decoded}
        subtitle="Group overview"
      />
      {/* TODO(frontend): full group detail (members, lag, offsets, reset) */}
    </div>
  );
}
