import { createFileRoute } from "@tanstack/react-router";
import { PageHeader } from "@/components/page-header";

export const Route = createFileRoute("/clusters/$cluster/brokers/$id")({
  component: BrokerDetail,
});

function BrokerDetail() {
  const { id } = Route.useParams();
  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={<span className="font-mono normal-case tracking-normal">Broker</span>}
        title={`Broker ${id}`}
      />
      {/* TODO(frontend): broker configs, log dirs, listeners */}
    </div>
  );
}
