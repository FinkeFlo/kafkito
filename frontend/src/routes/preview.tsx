import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { Boxes, Trash2 } from "lucide-react";
import { Button } from "@/components/button";
import { IconButton } from "@/components/icon-button";
import { Badge } from "@/components/badge";
import { LagBadge } from "@/components/lag-badge";
import { StatusDot } from "@/components/StatusDot";
import { Pill } from "@/components/pill";
import { Skeleton, SkeletonRows } from "@/components/skeleton";
import { Card, CardHeader, CardTitle } from "@/components/card";
import { Section } from "@/components/section";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/EmptyState";
import { Timestamp } from "@/components/timestamp";
import { RelativeTime } from "@/components/relative-time";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { toast } from "@/components/toaster";
import { DataTable } from "@/components/DataTable";
import { useTheme } from "@/lib/use-theme";
import { useTimeZone } from "@/lib/use-timezone";

export const Route = createFileRoute("/preview")({
  component: PreviewPage,
});

interface Row {
  id: string;
  name: string;
  partitions: number;
  rf: number;
  lag: number | null;
  ts: string;
}

const rows: Row[] = [
  { id: "1", name: "orders", partitions: 12, rf: 3, lag: 0, ts: "2026-04-22T19:34:01.041Z" },
  { id: "2", name: "billing-events", partitions: 6, rf: 3, lag: 412, ts: "2026-04-22T19:32:11.000Z" },
  { id: "3", name: "audit-trail", partitions: 24, rf: 3, lag: 5_412, ts: "2026-04-22T19:00:00.500Z" },
  { id: "4", name: "search-indexer", partitions: 3, rf: 1, lag: 142_000, ts: "2026-04-22T17:34:01.041Z" },
  { id: "5", name: "telemetry", partitions: 48, rf: 3, lag: null, ts: "2026-04-22T15:34:01.041Z" },
];

function PreviewPage() {
  const { theme, setPreference, preference } = useTheme();
  const [tz, setTz] = useTimeZone();
  const [confirmOpen, setConfirmOpen] = useState(false);

  return (
    <div className="space-y-10">
      <PageHeader
        title="Primitives preview"
        subtitle="Visual smoke test for all design-system primitives. Not part of the public navigation."
        actions={
          <>
            <Pill active={preference === "light"} onClick={() => setPreference("light")}>
              Light
            </Pill>
            <Pill active={preference === "dark"} onClick={() => setPreference("dark")}>
              Dark
            </Pill>
            <Pill active={preference === "system"} onClick={() => setPreference("system")}>
              System
            </Pill>
            <span className="px-2 text-xs text-subtle-text">
              resolved: {theme}
            </span>
          </>
        }
      />

      <Section title="Buttons">
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="primary">+ New Topic</Button>
          <Button variant="secondary">Refresh</Button>
          <Button variant="ghost">Cancel</Button>
          <Button variant="danger" leadingIcon={<Trash2 className="h-4 w-4" />}>
            Delete
          </Button>
          <Button variant="primary" size="sm">
            Small
          </Button>
          <Button variant="primary" loading>
            Loading
          </Button>
          <Button variant="primary" disabled>
            Disabled
          </Button>
          <IconButton
            aria-label="Delete topic"
            variant="danger"
            icon={<Trash2 className="h-4 w-4" />}
          />
        </div>
      </Section>

      <Section title="Badges">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="success">Reachable</Badge>
          <Badge variant="warning">Rebalancing</Badge>
          <Badge variant="danger">Unreachable</Badge>
          <Badge variant="neutral">INTERNAL</Badge>
          <Badge variant="info">SR</Badge>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-xs text-muted">Lag thresholds:</span>
          <LagBadge value={0} />
          <LagBadge value={500} />
          <LagBadge value={5_412} />
          <LagBadge value={142_000} />
          <LagBadge value={null} />
        </div>
        <div className="flex flex-wrap items-center gap-4">
          <span className="inline-flex items-center gap-2">
            <StatusDot intent="success" label="Reachable" />
            <span className="text-xs text-muted">Reachable</span>
          </span>
          <span className="inline-flex items-center gap-2">
            <StatusDot intent="danger" label="Unreachable" />
            <span className="text-xs text-muted">Unreachable</span>
          </span>
          <span className="inline-flex items-center gap-2">
            <StatusDot intent="neutral" label="Loading" pulsing />
            <span className="text-xs text-muted">Loading</span>
          </span>
        </div>
      </Section>

      <Section title="Pills">
        <div className="flex flex-wrap gap-2">
          <Pill active>local</Pill>
          <Pill>secure</Pill>
          <Pill>restricted</Pill>
        </div>
      </Section>

      <Section title="Skeletons">
        <Card>
          <SkeletonRows count={4} />
        </Card>
        <div className="flex items-center gap-3">
          <Skeleton width="w-12" height="h-12" rounded="full" />
          <div className="flex-1 space-y-2">
            <Skeleton width="w-1/3" />
            <Skeleton width="w-2/3" />
          </div>
        </div>
      </Section>

      <Section title="Card variants">
        <Card>
          <CardHeader>
            <CardTitle>Cluster: local</CardTitle>
            <Badge variant="success">Reachable</Badge>
          </CardHeader>
          <div className="px-6 py-4 text-sm text-muted">
            5 topics · 2 groups · Total lag 412
          </div>
        </Card>
        <Card compact>
          <p className="text-sm text-text">Compact card (p-4)</p>
        </Card>
      </Section>

      <Section title="Empty state">
        <Card flush>
          <EmptyState
            icon={<Boxes className="h-6 w-6" />}
            title="No topics yet"
            description="Create the first Kafka topic for this cluster."
            action={<Button>+ New Topic</Button>}
          />
        </Card>
      </Section>

      <Section title="Data table">
        <DataTable
          rows={rows}
          rowKey={(r) => r.id}
          onRowClick={(r) => toast(`Open ${r.name}`)}
          caption={`Showing ${rows.length} of ${rows.length}`}
          columns={[
            {
              id: "name",
              header: "Name",
              cell: (r) => (
                <span className="flex items-center gap-2">
                  <span className="font-mono text-[13px]">{r.name}</span>
                  {r.name.startsWith("__") && <Badge variant="neutral">INTERNAL</Badge>}
                </span>
              ),
              sortValue: (r) => r.name,
            },
            {
              id: "partitions",
              header: "Partitions",
              cell: (r) => r.partitions,
              sortValue: (r) => r.partitions,
              align: "right",
              className: "w-32",
            },
            {
              id: "rf",
              header: "RF",
              cell: (r) => r.rf,
              align: "right",
              className: "w-16",
            },
            {
              id: "lag",
              header: "Lag",
              cell: (r) => <LagBadge value={r.lag} />,
              sortValue: (r) => r.lag,
              align: "right",
              className: "w-32",
            },
            {
              id: "ts",
              header: "Last Commit",
              cell: (r) => <Timestamp value={r.ts} />,
              sortValue: (r) => r.ts,
              className: "w-72",
            },
          ]}
        />
        <DataTable
          rows={[]}
          rowKey={(r: Row) => r.id}
          columns={[
            { id: "name", header: "Name", cell: (r) => r.name },
          ]}
          emptyState={
            <EmptyState
              icon={<Boxes className="h-6 w-6" />}
              title="No rows"
              description="Empty result set."
            />
          }
        />
        <DataTable
          rows={undefined}
          rowKey={(r: Row) => r.id}
          isLoading
          columns={[
            { id: "name", header: "Name", cell: (r) => r.name },
            { id: "partitions", header: "Partitions", cell: (r) => r.partitions, align: "right" },
            { id: "lag", header: "Lag", cell: (r) => <LagBadge value={r.lag} />, align: "right" },
          ]}
        />
      </Section>

      <Section title="Time">
        <div className="flex flex-wrap items-center gap-4">
          <Pill active={tz === "utc"} onClick={() => setTz("utc")}>
            UTC
          </Pill>
          <Pill active={tz === "local"} onClick={() => setTz("local")}>
            Local
          </Pill>
        </div>
        <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
          <Card compact>
            <div className="text-xs text-muted">Timestamp</div>
            <Timestamp value="2026-04-22T19:34:01.041Z" />
          </Card>
          <Card compact>
            <div className="text-xs text-muted">Relative time</div>
            <RelativeTime value={Date.now() - 5 * 60_000} />
          </Card>
        </div>
      </Section>

      <Section title="Toaster + ConfirmDialog">
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" onClick={() => toast("Generic toast")}>
            Toast
          </Button>
          <Button variant="secondary" onClick={() => toast.success("Topic created")}>
            Success
          </Button>
          <Button variant="secondary" onClick={() => toast.error("Cluster unreachable")}>
            Error
          </Button>
          <Button variant="danger" onClick={() => setConfirmOpen(true)}>
            Open destructive dialog
          </Button>
        </div>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title='Delete topic "orders"?'
          description="This permanently removes the topic and all its messages."
          confirmPhrase="orders"
          confirmLabel="Delete topic"
          onConfirm={async () => {
            await new Promise((r) => setTimeout(r, 400));
            toast.success("Topic deleted");
          }}
        />
      </Section>
    </div>
  );
}
