import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { Boxes, Check, X } from "lucide-react";
import { toast } from "sonner";
import type { TopicInfo } from "@/lib/api";
import { useCluster } from "@/lib/use-cluster";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorState } from "@/components/ui/ErrorState";
import { FilterSelect } from "@/components/ui/FilterSelect";
import { KpiCard } from "@/components/ui/KpiCard";
import { PageHeader } from "@/components/ui/PageHeader";
import { SearchInput } from "@/components/ui/SearchInput";
import { Skeleton } from "@/components/ui/Skeleton";
import { Toolbar } from "@/components/ui/Toolbar";
import { TopicsTable } from "@/features/topics/TopicsTable";
import {
  filterTopics,
  hasKnownRetention,
  summarizeTopics,
  type PartitionBucket,
  type RetentionBucket,
} from "@/features/topics/topic-filters";
import { parseTopicListSearch, type TopicListSearch } from "@/features/topics/topic-list-params";
import { useFuzzy } from "@/lib/fuzzy";
import { claimOncePerSession } from "@/lib/once-per-session";
import { useFormatters } from "@/lib/use-formatters";
import { cn } from "@/lib/utils";
import { topicQueries } from "@/lib/queries/topics";

export const Route = createFileRoute("/clusters/$cluster/topics/")({
  validateSearch: parseTopicListSearch,
  component: TopicsPage,
});

const FUZZY_KEYS: (keyof TopicInfo)[] = ["name"];

const PARTITION_OPTIONS: { value: PartitionBucket | "any"; label: string }[] = [
  { value: "any", label: "Any" },
  { value: "1", label: "1" },
  { value: "2-10", label: "2–10" },
  { value: "gt10", label: "More than 10" },
];

const RETENTION_OPTIONS: { value: RetentionBucket | "any"; label: string }[] = [
  { value: "any", label: "Any" },
  { value: "le1d", label: "Up to 1 day" },
  { value: "le7d", label: "Up to 7 days" },
  { value: "gt7d", label: "More than 7 days" },
  { value: "infinite", label: "Infinite" },
  { value: "unknown", label: "Unknown" },
];

function TopicsPage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const { cluster, clusters } = useCluster();
  const clusterInfo = useMemo(() => clusters?.find((c) => c.name === cluster), [clusters, cluster]);
  const caps = clusterInfo?.capabilities;
  const fmt = useFormatters();

  const topicsQuery = useQuery({
    ...topicQueries.list(cluster!),
    enabled: !!cluster,
  });
  const topics = topicsQuery.data;

  // Re-parse the merged state so defaults ("", false, "any") drop out of the URL.
  const setSearch = (patch: Partial<Record<keyof TopicListSearch, unknown>>) =>
    navigate({ search: (prev) => parseTopicListSearch({ ...prev, ...patch }), replace: true });

  // Missing DESCRIBE_CONFIGS only costs the retention column, so it gets a
  // single toast per cluster and session instead of a permanent banner.
  const describeConfigsDenied = caps?.describe_configs === false;
  useEffect(() => {
    if (!cluster || !describeConfigsDenied) return;
    if (!claimOncePerSession(`topics.retention-denied.${cluster}`)) return;
    toast.warning("Retention not available", {
      description: (
        <>
          The Kafka user lacks <code className="font-mono">DESCRIBE_CONFIGS</code> on{" "}
          <code className="font-mono">TOPIC:*</code>.
        </>
      ),
      duration: 8000,
    });
  }, [cluster, describeConfigsDenied]);

  const all = topics ?? [];
  const internalCount = all.filter((t) => t.is_internal).length;
  const base = useMemo(
    () => filterTopics(topics ?? [], { showInternal: search.internal }),
    [topics, search.internal],
  );
  const retentionKnown = hasKnownRetention(base);
  const retentionDisabledReason =
    topics && base.length > 0 && !retentionKnown
      ? describeConfigsDenied
        ? "Retention filter unavailable: the Kafka user lacks DESCRIBE_CONFIGS on TOPIC:*."
        : "Retention filter unavailable: no topic reports its retention."
      : undefined;
  const retention = retentionDisabledReason ? undefined : search.retention;

  const filtered = useMemo(
    () =>
      filterTopics(topics ?? [], {
        showInternal: search.internal,
        partitions: search.partitions,
        retention,
      }),
    [topics, search.internal, search.partitions, retention],
  );
  const q = search.q ?? "";
  const fuzzy = useFuzzy(filtered, { keys: FUZZY_KEYS, query: q });
  const summary = summarizeTopics(base);
  const maxSize = Math.max(0, ...base.map((t) => t.size_bytes ?? 0));
  const filtersActive = !!q || !!search.partitions || !!retention;
  const clearFilters = () =>
    setSearch({ q: undefined, partitions: undefined, retention: undefined });

  const isLoading = topicsQuery.isLoading;
  const loadingValue = <Skeleton height="h-7" width="w-16" />;
  // Same height as the loaded delta line, so the cards do not grow when data arrives.
  const loadingDelta = <Skeleton height="h-3" width="w-24" className="my-0.5" />;
  const [sizeValue, sizeUnit] =
    summary.sizeBytes === null ? ["—", undefined] : splitUnit(fmt.bytes(summary.sizeBytes));

  const empty = filtersActive ? (
    <EmptyState
      icon={Boxes}
      title={
        q && !search.partitions && !retention
          ? `No topics match “${q}”`
          : "No topics match your filters"
      }
      description="Try a shorter name or loosen the partition and retention filters."
      action={
        <Button variant="secondary" size="sm" onClick={clearFilters}>
          Clear filters
        </Button>
      }
    />
  ) : (
    <EmptyState
      icon={Boxes}
      title="No topics yet"
      description={
        !search.internal && internalCount > 0
          ? `${internalCount} internal ${internalCount === 1 ? "topic is" : "topics are"} hidden.`
          : "This cluster has no topics."
      }
      action={
        !search.internal && internalCount > 0 ? (
          <Button variant="secondary" size="sm" onClick={() => setSearch({ internal: true })}>
            Show internal
          </Button>
        ) : undefined
      }
    />
  );

  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={
          <>
            <span className="font-mono normal-case tracking-normal">{cluster ?? "—"}</span>{" "}
            <span aria-hidden>›</span> Topics
          </>
        }
        title="Topics"
        subtitle="Browse and filter the topics in this cluster."
      />

      {!cluster && (
        <EmptyState
          icon={Boxes}
          title="No cluster selected"
          description="Pick a cluster from the header to browse its topics."
        />
      )}

      {cluster && topicsQuery.isError && (
        <ErrorState
          title="Failed to load topics"
          detail={(topicsQuery.error as Error | null)?.message}
          onRetry={() => topicsQuery.refetch()}
        />
      )}

      {cluster && !topicsQuery.isError && (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <KpiCard
              label="Topics"
              value={isLoading ? loadingValue : fmt.number(summary.topics)}
              delta={
                isLoading
                  ? loadingDelta
                  : internalCount === 0
                    ? undefined
                    : search.internal
                      ? `incl. ${internalCount} internal`
                      : `${internalCount} internal hidden`
              }
            />
            <KpiCard
              label="Partitions"
              value={isLoading ? loadingValue : fmt.number(summary.partitions)}
              delta={
                isLoading
                  ? loadingDelta
                  : summary.topics === 0
                    ? undefined
                    : `avg ${fmt.decimal(summary.partitions / summary.topics, 1)} per topic`
              }
            />
            <KpiCard
              label="Retained"
              value={isLoading ? loadingValue : sizeValue}
              unit={isLoading ? undefined : sizeUnit}
              delta={
                isLoading ? (
                  loadingDelta
                ) : !summary.largest ? undefined : (
                  <>
                    Largest <span className="font-mono">{summary.largest}</span>
                  </>
                )
              }
            />
            <KpiCard
              label="Throughput"
              value={
                isLoading
                  ? loadingValue
                  : summary.ratePerSec === null
                    ? "—"
                    : fmt.rate(summary.ratePerSec)
              }
              delta={
                isLoading
                  ? loadingDelta
                  : summary.ratePerSec === null
                    ? undefined
                    : `${summary.idle} ${summary.idle === 1 ? "topic" : "topics"} idle`
              }
            />
          </div>

          <Toolbar
            search={
              <SearchInput
                value={q}
                onChange={(v) => setSearch({ q: v })}
                placeholder="Filter topics by name…"
                ariaLabel="Filter topics"
                count={{ visible: fuzzy.results.length, total: base.length }}
              />
            }
            filters={
              <>
                <FilterSelect
                  label="Partitions"
                  value={search.partitions ?? "any"}
                  options={PARTITION_OPTIONS}
                  onChange={(v) => setSearch({ partitions: v })}
                />
                <FilterSelect
                  label="Retention"
                  value={retention ?? "any"}
                  options={RETENTION_OPTIONS}
                  onChange={(v) => setSearch({ retention: v })}
                  disabledReason={retentionDisabledReason}
                />
                {/* A toggle button rather than a bare checkbox: the whole h-9 pill
                    is the target, so it stays operable when a popover covers part
                    of it (WCAG 2.5.8). The check mark carries the state, not colour. */}
                <button
                  type="button"
                  aria-pressed={!!search.internal}
                  onClick={() => setSearch({ internal: !search.internal })}
                  className={cn(
                    "flex h-9 items-center gap-2 rounded-md border bg-panel px-3 text-xs text-muted transition-colors hover:text-text",
                    search.internal
                      ? "border-border-strong text-text"
                      : "border-border hover:border-border-hover",
                  )}
                >
                  <span
                    aria-hidden="true"
                    className={cn(
                      "grid h-3.5 w-3.5 place-items-center rounded-sm border",
                      search.internal
                        ? "border-accent bg-accent text-accent-foreground"
                        : "border-border-strong",
                    )}
                  >
                    {search.internal ? <Check className="h-3 w-3" strokeWidth={3} /> : null}
                  </span>
                  Show internal
                  {internalCount > 0 ? (
                    <span aria-hidden="true" className="font-mono text-[11px] text-subtle-text">
                      {internalCount}
                    </span>
                  ) : null}
                </button>
                {filtersActive ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    leadingIcon={<X className="h-4 w-4" aria-hidden />}
                    onClick={clearFilters}
                  >
                    Clear filters
                  </Button>
                ) : null}
              </>
            }
          />

          {!isLoading && topics && fuzzy.results.length === 0 ? (
            empty
          ) : (
            <TopicsTable
              cluster={cluster}
              rows={topics ? fuzzy.results : undefined}
              isLoading={isLoading}
              rangesFor={(t) => fuzzy.rangesFor(t, "name")}
              maxSize={maxSize}
              sort={search.sort ? { key: search.sort, dir: search.dir ?? "asc" } : null}
              onSortChange={(next) => setSearch({ sort: next?.key, dir: next?.dir })}
            />
          )}
        </>
      )}
    </div>
  );
}

/** "18.4 GB" → ["18.4", "GB"], so the KPI shows the unit next to the number. */
function splitUnit(formatted: string): [string, string | undefined] {
  const i = formatted.lastIndexOf(" ");
  return i < 0 ? [formatted, undefined] : [formatted.slice(0, i), formatted.slice(i + 1)];
}
