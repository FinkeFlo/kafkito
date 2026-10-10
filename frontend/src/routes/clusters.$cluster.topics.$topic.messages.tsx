import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { topicQueries } from "@/lib/queries/topics";
import type { BrowseFrom } from "@/features/messages/browse-params";
import { BulkCopySection } from "@/features/messages/BulkCopySection";
import { MessagesPanel } from "@/features/messages/MessagesPanel";

interface MessagesSearch {
  partition: number;
  limit: number;
  from: BrowseFrom;
  msgOffset: number;
}

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/messages")({
  validateSearch: (s: Record<string, unknown>): MessagesSearch => {
    const fromRaw = s.from;
    const from: BrowseFrom = fromRaw === "start" || fromRaw === "offset" ? fromRaw : "end";
    return {
      partition: typeof s.partition === "number" ? s.partition : -1,
      limit: typeof s.limit === "number" ? s.limit : 50,
      from,
      msgOffset: typeof s.msgOffset === "number" ? s.msgOffset : 0,
    };
  },
  component: MessagesTab,
});

function MessagesTab() {
  const { cluster, topic } = Route.useParams();
  const [copyOpen, setCopyOpen] = useState(false);

  const detailQuery = useQuery({
    ...topicQueries.detail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: 5_000,
  });

  if (!detailQuery.data) {
    return <div className="text-sm text-muted">Loading…</div>;
  }

  const partitionNumbers = detailQuery.data.partitions.map((p) => p.partition);

  return (
    <div className="space-y-4">
      <MessagesPanel cluster={cluster} topic={topic} partitions={detailQuery.data.partitions} />

      <BulkCopySection
        cluster={cluster}
        topic={topic}
        partitions={partitionNumbers}
        open={copyOpen}
        onToggle={() => setCopyOpen((v) => !v)}
      />
    </div>
  );
}
