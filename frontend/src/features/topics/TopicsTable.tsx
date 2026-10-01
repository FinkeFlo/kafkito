import { Link } from "@tanstack/react-router";
import type { TopicInfo } from "@/lib/api";
import { DataTable, type DataTableColumn, type DataTableSort } from "@/components/ui/DataTable";
import { Highlight } from "@/components/ui/Highlight";
import { Tag } from "@/components/ui/Tag";
import { LagBadge } from "@/features/groups/LagBadge";
import type { HighlightRange } from "@/lib/fuzzy";
import { useFormatters, type Formatters } from "@/lib/use-formatters";
import { IDLE_RATE } from "./topic-filters";

const NUM = "font-mono text-[13px] tabular-nums";

function Unknown({ what }: { what: string }) {
  return (
    <span className="text-subtle-text">
      <span aria-hidden="true">—</span>
      <span className="sr-only">unknown {what}</span>
    </span>
  );
}

function known(v: number | null | undefined): v is number {
  return v !== null && v !== undefined;
}

/** Size with a bar relative to the largest topic, so outliers stand out when scanning. */
function SizeCell({ bytes, max, fmt }: { bytes?: number; max: number; fmt: Formatters }) {
  if (!known(bytes)) return <Unknown what="size" />;
  const pct = max > 0 ? (bytes / max) * 100 : 0;
  return (
    <span className="inline-flex items-center justify-end gap-2.5">
      <span aria-hidden="true" className="h-1 w-14 overflow-hidden rounded-full bg-subtle">
        <span
          className="block h-full rounded-full bg-border-strong"
          // Keep a visible sliver for small but non-empty topics.
          style={{ width: `${bytes > 0 ? Math.max(pct, 3) : 0}%` }}
        />
      </span>
      <span className="min-w-[4.5rem]">{fmt.bytes(bytes)}</span>
    </span>
  );
}

function RateCell({ rate, fmt }: { rate?: number; fmt: Formatters }) {
  if (!known(rate)) return <Unknown what="rate" />;
  if (rate < IDLE_RATE) return <span className="font-sans text-xs text-subtle-text">idle</span>;
  return <>{fmt.rate(rate)}</>;
}

function RetentionCell({ ms, fmt }: { ms?: number; fmt: Formatters }) {
  if (!known(ms)) return <Unknown what="retention" />;
  if (ms < 0) {
    return (
      <>
        <span aria-hidden="true">∞</span>
        <span className="sr-only">infinite</span>
      </>
    );
  }
  return <>{fmt.duration(ms)}</>;
}

export function TopicsTable({
  cluster,
  rows,
  isLoading,
  rangesFor,
  maxSize,
  sort,
  onSortChange,
}: {
  cluster: string;
  rows: TopicInfo[] | undefined;
  isLoading: boolean;
  rangesFor: (topic: TopicInfo) => readonly HighlightRange[];
  /** Largest known size among the listed topics; scales the size bars. */
  maxSize: number;
  sort: DataTableSort | null;
  onSortChange: (next: DataTableSort | null) => void;
}) {
  const fmt = useFormatters();
  const columns: DataTableColumn<TopicInfo>[] = [
    {
      id: "name",
      header: "Topic",
      sortValue: (t) => t.name,
      cell: (t) => (
        <span className="flex items-center gap-2">
          <Link
            to="/clusters/$cluster/topics/$topic"
            params={{ cluster, topic: t.name }}
            data-row-primary=""
            className="font-mono text-[13px] font-medium text-text hover:underline hover:underline-offset-2"
          >
            <Highlight text={t.name} ranges={rangesFor(t)} />
          </Link>
          {t.is_internal && <Tag className="bg-transparent text-subtle-text">INTERNAL</Tag>}
        </span>
      ),
    },
    {
      id: "partitions",
      header: "Partitions",
      align: "right",
      sortValue: (t) => t.partitions,
      cell: (t) => <span className={NUM}>{t.partitions}</span>,
    },
    {
      id: "rf",
      header: "RF",
      align: "right",
      sortValue: (t) => t.replication_factor,
      cell: (t) => <span className={NUM}>{t.replication_factor}</span>,
    },
    {
      id: "messages",
      header: "Messages",
      align: "right",
      sortValue: (t) => t.messages,
      cell: (t) => (
        <span className={NUM}>
          {known(t.messages) ? fmt.count(t.messages) : <Unknown what="message count" />}
        </span>
      ),
    },
    {
      id: "size",
      header: "Size",
      align: "right",
      sortValue: (t) => t.size_bytes,
      cell: (t) => (
        <span className={NUM}>
          <SizeCell bytes={t.size_bytes} max={maxSize} fmt={fmt} />
        </span>
      ),
    },
    {
      id: "rate",
      header: "Rate",
      align: "right",
      sortValue: (t) => t.rate_per_sec,
      cell: (t) => (
        <span className={NUM}>
          <RateCell rate={t.rate_per_sec} fmt={fmt} />
        </span>
      ),
    },
    {
      id: "lag",
      header: "Lag",
      align: "right",
      sortValue: (t) => t.lag,
      cell: (t) => <LagBadge value={t.lag} />,
    },
    {
      id: "retention",
      header: "Retention",
      align: "right",
      // Infinite (-1) sorts after every finite retention.
      sortValue: (t) => (known(t.retention_ms) && t.retention_ms < 0 ? Infinity : t.retention_ms),
      cell: (t) => (
        <span className={NUM}>
          <RetentionCell ms={t.retention_ms} fmt={fmt} />
        </span>
      ),
    },
  ];

  return (
    <DataTable<TopicInfo>
      columns={columns}
      rows={rows}
      rowKey={(t) => t.name}
      isLoading={isLoading}
      clickableRows
      sort={sort}
      onSortChange={onSortChange}
    />
  );
}
