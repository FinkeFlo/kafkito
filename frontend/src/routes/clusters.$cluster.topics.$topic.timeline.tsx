import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchMessageTimeline, fetchTopicDetail, type MessageTimelineBucket } from "@/lib/api";
import { useFormatters } from "@/lib/use-formatters";
import { Timestamp } from "@/components/timestamp";
import { useTimeZone } from "@/lib/use-timezone";

type Preset = "24h" | "7d" | "30d";

const HOUR_MS = 60 * 60_000;
const DAY_MS = 24 * HOUR_MS;

function presetToMs(preset: Preset): number {
  switch (preset) {
    case "24h":
      return HOUR_MS * 24;
    case "30d":
      return DAY_MS * 30;
    case "7d":
    default:
      return DAY_MS * 7;
  }
}

/** 24h range buckets by hour, everything else buckets by day. */
function presetToBucketMs(preset: Preset): number {
  return preset === "24h" ? HOUR_MS : DAY_MS;
}

const PRESET_OPTIONS: ReadonlyArray<{ key: Preset; label: string }> = [
  { key: "24h", label: "Last 24 hours" },
  { key: "7d", label: "Last 7 days" },
  { key: "30d", label: "Last 30 days" },
];

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/timeline")({
  validateSearch: (s: Record<string, unknown>): { preset: Preset; partition: number } => ({
    preset: s.preset === "24h" || s.preset === "7d" || s.preset === "30d" ? s.preset : "7d",
    partition: typeof s.partition === "number" ? s.partition : -1,
  }),
  component: TopicTimelinePage,
});

function TopicTimelinePage() {
  const { cluster, topic } = Route.useParams();
  const fmt = useFormatters();
  const { preset, partition } = Route.useSearch();
  const navigate = Route.useNavigate();
  const setPreset = (p: Preset) => navigate({ search: (prev) => ({ ...prev, preset: p }) });
  const setPartition = (p: number) => navigate({ search: (prev) => ({ ...prev, partition: p }) });
  const [selected, setSelected] = useState<number | null>(null);

  const range = useMemo(() => {
    const to_ts_ms = Date.now();
    const bucket_ms = presetToBucketMs(preset);
    return {
      from_ts_ms: to_ts_ms - presetToMs(preset),
      to_ts_ms,
      bucket_ms,
    };
  }, [preset]);

  const topicQuery = useQuery({
    queryKey: ["topic", cluster, topic],
    queryFn: () => fetchTopicDetail(cluster, topic),
    enabled: !!cluster,
    staleTime: 10_000,
  });

  const timelineQuery = useQuery({
    queryKey: ["message-timeline", cluster, topic, partition, range],
    queryFn: () =>
      fetchMessageTimeline(cluster, topic, {
        partition,
        from_ts_ms: range.from_ts_ms,
        to_ts_ms: range.to_ts_ms,
        bucket_ms: range.bucket_ms,
      }),
    enabled: !!cluster,
    staleTime: 10_000,
  });

  const buckets = timelineQuery.data?.buckets ?? [];
  const total = buckets.reduce((sum, b) => sum + b.approx_count, 0);
  const max = Math.max(1, ...buckets.map((b) => b.approx_count));

  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-4">
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
            <label>Range</label>
            <div className="inline-flex overflow-hidden rounded border border-[var(--color-border)]">
              {PRESET_OPTIONS.map((p, i) => (
                <button
                  key={p.key}
                  type="button"
                  onClick={() => {
                    setPreset(p.key);
                    setSelected(null);
                  }}
                  aria-pressed={preset === p.key}
                  title={`Buckets: ${p.key === "24h" ? "hourly" : "daily"}`}
                  className={`px-2 py-1 text-xs ${i > 0 ? "border-l border-[var(--color-border)]" : ""} ${
                    preset === p.key
                      ? "bg-accent-subtle text-accent"
                      : "text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
                  }`}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
            <label>Partition</label>
            <select
              value={partition}
              onChange={(e) => {
                setPartition(Number(e.target.value));
                setSelected(null);
              }}
              className="rounded border border-[var(--color-border)] px-2 py-1 text-xs"
            >
              <option value={-1}>all</option>
              {topicQuery.data?.partitions.map((p) => (
                <option key={p.partition} value={p.partition}>
                  {p.partition}
                </option>
              ))}
            </select>
          </div>
          <div className="ml-auto text-sm text-[var(--color-text-muted)]">
            {timelineQuery.isLoading && "Loading…"}
            {timelineQuery.isError && (
              <span className="text-[var(--color-danger)]">
                Failed to load: {(timelineQuery.error as Error).message}
              </span>
            )}
            {!timelineQuery.isLoading && !timelineQuery.isError && (
              <>
                approx total:{" "}
                <span className="font-semibold text-[var(--color-text)]">
                  {fmt.number(total)}
                </span>
              </>
            )}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1fr)_380px] xl:items-start">
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-4">
          {timelineQuery.isLoading && (
            <div className="py-8 text-center text-sm text-[var(--color-text-muted)]">
              Loading timeline…
            </div>
          )}
          {!timelineQuery.isLoading && buckets.length === 0 && !timelineQuery.isError && (
            <div className="py-8 text-center text-sm text-[var(--color-text-muted)]">
              No data in the selected range.
            </div>
          )}
          {buckets.length > 0 && (
            <TimelineBarChart
              buckets={buckets}
              max={max}
              selected={selected}
              onSelect={setSelected}
              preset={preset}
            />
          )}
        </div>

        {buckets.length > 0 && (
          <TimelineDetailTable
            buckets={buckets}
            highlighted={selected}
            onSelect={setSelected}
          />
        )}
      </div>
    </div>
  );
}

function bucketLabel(b: MessageTimelineBucket, preset: Preset, zone: "utc" | "local"): string {
  const d = new Date(b.from_ts_ms);
  if (preset === "24h") {
    return zone === "utc"
      ? `${d.getUTCHours().toString().padStart(2, "0")}:00`
      : `${d.getHours().toString().padStart(2, "0")}:00`;
  }
  const y = zone === "utc" ? d.getUTCFullYear() : d.getFullYear();
  const m = (zone === "utc" ? d.getUTCMonth() : d.getMonth()) + 1;
  const day = zone === "utc" ? d.getUTCDate() : d.getDate();
  return `${y}-${String(m).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
}

function TimelineBarChart({
  buckets,
  max,
  selected,
  onSelect,
  preset,
}: {
  buckets: MessageTimelineBucket[];
  max: number;
  selected: number | null;
  onSelect: (i: number | null) => void;
  preset: Preset;
}) {
  const fmt = useFormatters();
  const [zone] = useTimeZone();
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState(0);
  const [hovered, setHovered] = useState<number | null>(null);

  // Measure the actual available width so the chart always fills its card
  // instead of a hardcoded reference width.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const update = () => setContainerWidth(el.clientWidth);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const chartH = 140;
  const barGap = 2;
  const available = containerWidth || 600;
  const barW = Math.max(2, available / buckets.length - barGap);
  const chartW = Math.max(available, buckets.length * (barW + barGap));
  // Show at most ~10 x-axis labels; skip the rest to avoid overlap on
  // ranges with many buckets (e.g. 30 daily buckets).
  const labelStep = Math.max(1, Math.ceil(buckets.length / 15));

  return (
    <div ref={containerRef} className="relative w-full overflow-x-auto">
      <svg
        width={chartW}
        height={chartH + 26}
        role="img"
        aria-label="Message count per bucket"
      >
        {buckets.map((b, i) => {
          const h = Math.round((b.approx_count / max) * (chartH - 8));
          const x = i * (barW + barGap);
          const y = chartH - h;
          const isSelected = selected === i;
          const isHovered = hovered === i;
          return (
            <g
              key={b.from_ts_ms}
              onClick={() => onSelect(isSelected ? null : i)}
              onMouseEnter={() => setHovered(i)}
              onMouseLeave={() => setHovered(null)}
              className="cursor-pointer"
            >
              <rect
                x={x}
                y={y}
                width={barW}
                height={Math.max(1, h)}
                rx={2}
                fill="var(--color-accent)"
                opacity={isSelected || isHovered ? 1 : 0.65}
                className="transition-opacity"
              />
              {i % labelStep === 0 && (
                <text
                  x={x + barW / 2}
                  y={chartH + 14}
                  textAnchor="middle"
                  fontSize={9}
                  fill="var(--color-text-muted)"
                >
                  {bucketLabel(b, preset, zone).slice(-5)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      {hovered !== null &&
        (() => {
          const b = buckets[hovered];
          const x = hovered * (barW + barGap) + barW / 2;
          return (
            <div
              className="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-1/2 whitespace-nowrap rounded-md border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-2 py-1 text-xs text-[var(--color-text)] shadow-md"
              style={{ left: x, top: chartH / 2 }}
            >
              <div className="font-medium">{bucketLabel(b, preset, zone)}</div>
              <div className="text-[var(--color-text-muted)]">
                {fmt.number(b.approx_count)} messages
              </div>
            </div>
          );
        })()}
    </div>
  );
}

function TimelineDetailTable({
  buckets,
  highlighted,
  onSelect,
}: {
  buckets: MessageTimelineBucket[];
  highlighted: number | null;
  onSelect: (i: number | null) => void;
}) {
  const fmt = useFormatters();
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-4">
      <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-[var(--color-text-muted)]">
        Time grid
      </div>
      <div className="max-h-64 overflow-x-auto overflow-y-auto">
        <table className="w-full table-fixed font-mono text-xs">
          <thead className="sticky top-0 bg-[var(--color-surface-raised)] text-[10px] uppercase tracking-wider text-[var(--color-text-muted)]">
            <tr>
              <th className="w-2/3 text-left">from</th>
              <th className="w-1/3 pl-3 text-right">≈ count</th>
            </tr>
          </thead>
          <tbody>
            {buckets.map((b, i) => (
              <tr
                key={b.from_ts_ms}
                onClick={() => onSelect(highlighted === i ? null : i)}
                title={`${b.from_ts_ms} – ${b.to_ts_ms}`}
                className={
                  "cursor-pointer " +
                  (highlighted === i
                    ? "bg-[var(--color-surface-subtle)]"
                    : "hover:bg-[var(--color-surface-subtle)]")
                }
              >
                <td className="truncate">
                  <Timestamp value={b.from_ts_ms} />
                </td>
                <td className="pl-3 text-right">{fmt.number(b.approx_count)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
