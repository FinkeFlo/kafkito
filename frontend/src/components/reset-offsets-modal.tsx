import { useEffect, useMemo, useState } from "react";
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  resetGroupOffsets,
  type GroupDetail,
  type ResetOffsetResult,
  type ResetStrategy,
} from "@/lib/api";
import { Button } from "@/components/button";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Input } from "@/components/Input";
import { Modal } from "@/components/Modal";
import { Notice } from "@/components/Notice";
import { Timestamp } from "@/components/timestamp";
import { LagBadge } from "@/components/lag-badge";
import { localInputToMs, msToLocalInput } from "@/lib/datetime";

function partitionsForTopic(detail: GroupDetail, topic: string): number[] {
  return detail.offsets
    .filter((o) => o.topic === topic)
    .map((o) => o.partition);
}

function selectAll(parts: number[]): Record<number, boolean> {
  return Object.fromEntries(parts.map((p) => [p, true]));
}

export function ResetOffsetsModal({
  cluster,
  detail,
  isProd = false,
  onClose,
}: {
  cluster: string;
  detail: GroupDetail;
  isProd?: boolean;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const topics = useMemo(() => {
    const s = new Set<string>();
    for (const o of detail.offsets) s.add(o.topic);
    return Array.from(s).sort();
  }, [detail.offsets]);

  const [topic, setTopic] = useState(topics[0] ?? "");
  const [strategy, setStrategy] = useState<ResetStrategy>("earliest");
  const [offset, setOffset] = useState("0");
  const [timestampMs, setTimestampMs] = useState(String(Date.now() - 3600_000));
  const [shift, setShift] = useState("-100");
  // All partitions of the initial topic are selected by default; resetting the
  // whole consumer group is the common case.
  const [partSel, setPartSel] = useState<Record<number, boolean>>(() =>
    selectAll(partitionsForTopic(detail, topics[0] ?? "")),
  );
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<ResetOffsetResult[] | null>(null);
  const [commitOpen, setCommitOpen] = useState(false);

  const topicParts = useMemo(
    () =>
      detail.offsets
        .filter((o) => o.topic === topic)
        .map((o) => o.partition)
        .sort((a, b) => a - b),
    [detail.offsets, topic],
  );

  const selectedParts = topicParts.filter((p) => partSel[p]);

  const timestampNum = Number(timestampMs);
  const timestampValid =
    timestampMs.trim() !== "" &&
    Number.isFinite(timestampNum) &&
    timestampNum > 0;
  const localValue = timestampValid ? msToLocalInput(timestampNum) : "";

  const offsetValid =
    strategy !== "offset" ||
    (offset.trim() !== "" && Number.isFinite(Number(offset)));
  const shiftValid =
    strategy !== "shift-by" ||
    (shift.trim() !== "" && Number.isFinite(Number(shift)));
  const strategyReady =
    (strategy === "timestamp" ? timestampValid : true) &&
    offsetValid &&
    shiftValid;

  const setRelativeHours = (hours: number) => {
    setTimestampMs(String(Date.now() - hours * 3600_000));
  };

  // Preview always covers every partition of the topic so the projection is
  // visible before any selection. Partition selection only governs the commit.
  const previewBody = useMemo(
    () => ({
      topic,
      strategy,
      offset: strategy === "offset" ? Number(offset) : undefined,
      timestamp_ms: strategy === "timestamp" ? Number(timestampMs) : undefined,
      shift: strategy === "shift-by" ? Number(shift) : undefined,
    }),
    [topic, strategy, offset, timestampMs, shift],
  );

  // Debounce the preview inputs so typing doesn't fire a dry-run per keystroke.
  const [debouncedBody, setDebouncedBody] = useState(previewBody);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedBody(previewBody), 400);
    return () => clearTimeout(t);
  }, [previewBody]);

  const previewQuery = useQuery({
    queryKey: ["reset-offsets-preview", cluster, detail.group_id, debouncedBody],
    queryFn: () =>
      resetGroupOffsets(cluster, detail.group_id, {
        ...debouncedBody,
        dry_run: true,
      }, isProd),
    enabled: !!topic && strategyReady,
    placeholderData: keepPreviousData,
    staleTime: 10_000,
    retry: false,
  });
  const previewRows = previewQuery.data?.results ?? null;

  const buildBody = (dry_run: boolean) => ({
    topic,
    partitions: selectedParts.length > 0 ? selectedParts : undefined,
    strategy,
    offset: strategy === "offset" ? Number(offset) : undefined,
    timestamp_ms: strategy === "timestamp" ? Number(timestampMs) : undefined,
    shift: strategy === "shift-by" ? Number(shift) : undefined,
    dry_run,
  });

  const commitMut = useMutation({
    mutationFn: () => resetGroupOffsets(cluster, detail.group_id, buildBody(false), isProd),
    onSuccess: (r) => {
      setResult(r.results);
      qc.invalidateQueries({ queryKey: ["group", cluster, detail.group_id] });
      qc.invalidateQueries({ queryKey: ["groups", cluster] });
    },
    onError: (e: Error) => setErr(e.message),
  });

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={
        <>
          Reset offsets — <span className="font-mono">{detail.group_id}</span>
        </>
      }
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={
              commitMut.isPending || selectedParts.length === 0 || !strategyReady
            }
            onClick={() => setCommitOpen(true)}
          >
            {commitMut.isPending ? "Committing…" : "Commit reset"}
          </Button>
          <ConfirmDialog
            open={commitOpen}
            onOpenChange={setCommitOpen}
            variant="primary"
            title="Commit new offsets?"
            description={`${isProd ? "⚠ Production cluster — " : ""}This will overwrite committed offsets for group "${detail.group_id}" on topic "${topic}". Partitions: ${selectedParts.join(",")} (${selectedParts.length} of ${topicParts.length}).`}
            confirmPhrase={detail.group_id}
            confirmLabel="Commit reset"
            onConfirm={async () => {
              setErr(null);
              await commitMut.mutateAsync();
            }}
          />
        </>
      }
    >
      <div className="space-y-4 text-sm">
        <div className="grid grid-cols-2 gap-4">
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Topic
            </span>
            <select
              value={topic}
              onChange={(e) => {
                setTopic(e.target.value);
                setPartSel(selectAll(partitionsForTopic(detail, e.target.value)));
              }}
              className="mt-1 h-9 w-full rounded-md border border-border bg-panel px-2 font-mono text-sm hover:border-border-hover"
            >
              {topics.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </label>
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Strategy
            </span>
            <select
              value={strategy}
              onChange={(e) => setStrategy(e.target.value as ResetStrategy)}
              className="mt-1 h-9 w-full rounded-md border border-border bg-panel px-2 text-sm hover:border-border-hover"
            >
              <option value="earliest">earliest (log start)</option>
              <option value="latest">latest (log end)</option>
              <option value="offset">specific offset</option>
              <option value="timestamp">timestamp</option>
              <option value="shift-by">shift-by (delta)</option>
            </select>
          </label>
        </div>
        {strategy === "offset" && (
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Offset
            </span>
            <Input
              value={offset}
              onChange={(e) => setOffset(e.target.value)}
              className="mt-1 font-mono"
            />
            <span className="mt-1 block text-xs text-muted">
              Applied to every selected partition. Clamped to [start, end].
            </span>
          </label>
        )}
        {strategy === "timestamp" && (
          <div className="space-y-2">
            <div className="block">
              <span className="text-xs font-semibold uppercase tracking-wider text-muted">
                Date &amp; time
              </span>
              <div className="mt-1 flex flex-wrap items-center gap-2">
                <label htmlFor="reset-offsets-timestamp" className="sr-only">
                  Date &amp; time
                </label>
                <Input
                  id="reset-offsets-timestamp"
                  type="datetime-local"
                  step="1"
                  value={localValue}
                  onChange={(e) => {
                    const ms = localInputToMs(e.target.value);
                    setTimestampMs(Number.isFinite(ms) ? String(ms) : "");
                  }}
                  className="max-w-[16rem] font-mono"
                />
                <div className="flex gap-1.5">
                  {[
                    { label: "-1h", hours: 1 },
                    { label: "-6h", hours: 6 },
                    { label: "-24h", hours: 24 },
                  ].map((q) => (
                    <button
                      key={q.label}
                      type="button"
                      onClick={() => setRelativeHours(q.hours)}
                      className="rounded border border-border bg-panel px-2 py-1 text-xs font-mono text-muted hover:border-border-hover hover:text-text"
                    >
                      {q.label}
                    </button>
                  ))}
                </div>
              </div>
            </div>
            {timestampValid ? (
              <p className="text-xs text-muted">
                Resolves to <Timestamp value={timestampNum} zone="utc" /> (UTC)
              </p>
            ) : (
              <Notice intent="warning">
                Pick a valid date and time.
              </Notice>
            )}
          </div>
        )}
        {strategy === "shift-by" && (
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Shift (records, negative allowed)
            </span>
            <Input
              value={shift}
              onChange={(e) => setShift(e.target.value)}
              className="mt-1 font-mono"
            />
          </label>
        )}
        <div>
          <div className="mb-1 flex items-center justify-between text-xs">
            <span className="font-semibold uppercase tracking-wider text-muted">
              Partitions
            </span>
            <div className="flex gap-2 text-xs">
              <button
                type="button"
                onClick={() =>
                  setPartSel(Object.fromEntries(topicParts.map((p) => [p, true])))
                }
                className="text-muted hover:text-text"
              >
                all
              </button>
              <button
                type="button"
                onClick={() => setPartSel({})}
                className="text-muted hover:text-text"
              >
                none
              </button>
            </div>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {topicParts.map((p) => {
              const partitionId = `reset-offsets-partition-${p}`;
              return (
                <label
                  key={p}
                  htmlFor={partitionId}
                  className={[
                    "flex cursor-pointer items-center gap-1 rounded border px-2 py-0.5 text-xs font-mono",
                    partSel[p]
                      ? "border-accent bg-accent text-accent-foreground"
                      : "border-border bg-panel text-text hover:border-border-hover",
                  ].join(" ")}
                >
                  <input
                    id={partitionId}
                    type="checkbox"
                    checked={!!partSel[p]}
                    onChange={(e) =>
                      setPartSel((s) => ({ ...s, [p]: e.target.checked }))
                    }
                    className="sr-only"
                  />
                  p{p}
                </label>
              );
            })}
          </div>
          {selectedParts.length === 0 ? (
            <div className="mt-2">
              <Notice intent="warning">Pick at least one partition.</Notice>
            </div>
          ) : (
            <div className="mt-1 text-xs text-muted">
              {`${selectedParts.length} of ${topicParts.length} selected`}
            </div>
          )}
        </div>

        <div className="rounded-md border border-border bg-subtle p-2 text-xs">
          <div className="mb-1 flex items-center justify-between">
            <span className="font-semibold">
              {result ? "Committed" : "Lag preview"}
            </span>
            {!result && previewQuery.isFetching && (
              <span className="text-[10px] text-subtle-text">updating…</span>
            )}
          </div>
          {(() => {
            const committed = !!result;
            const rows = result ?? previewRows;

            if (!committed && !strategyReady) {
              return (
                <p className="text-subtle-text">
                  Enter a valid value to preview the projected lag.
                </p>
              );
            }
            if (!committed && previewQuery.isError) {
              return (
                <p className="text-danger">
                  {(previewQuery.error as Error).message}
                </p>
              );
            }
            if (!rows) {
              return <p className="text-subtle-text">Calculating preview…</p>;
            }
            if (rows.length === 0) {
              return (
                <p className="text-subtle-text">No partitions to preview.</p>
              );
            }

            // Lag after the operation for a partition: selected (or committed)
            // partitions move to the new offset, everything else keeps its
            // current committed offset.
            const lagAfter = (r: ResetOffsetResult): number | null => {
              const base =
                committed || partSel[r.partition] ? r.new_offset : r.old_offset;
              if (r.error || base < 0 || r.end_offset < 0) return null;
              return Math.max(0, r.end_offset - base);
            };
            const known = rows
              .map(lagAfter)
              .filter((l): l is number => l !== null);
            const totalLag =
              known.length > 0 ? known.reduce((a, b) => a + b, 0) : null;

            return (
              <>
                <table className="w-full font-mono">
                  <thead className="text-[10px] uppercase tracking-wider text-subtle-text">
                    <tr>
                      <th className="text-left">partition</th>
                      <th className="text-right">old</th>
                      <th className="text-right">→ new</th>
                      <th className="text-right pl-4">end</th>
                      <th className="text-right pl-4">≈ lag after</th>
                      <th className="text-left pl-4">error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((r) => {
                      const lag = lagAfter(r);
                      const active = committed || !!partSel[r.partition];
                      return (
                        <tr
                          key={r.partition}
                          className={active ? "" : "text-subtle-text"}
                        >
                          <td className={active ? "text-accent" : undefined}>
                            p{r.partition}
                          </td>
                          <td className="text-right text-muted">
                            {r.old_offset >= 0 ? r.old_offset : "—"}
                          </td>
                          <td className="text-right">
                            {r.new_offset >= 0 ? r.new_offset : "—"}
                          </td>
                          <td className="text-right pl-4 text-muted">
                            {r.end_offset >= 0 ? r.end_offset : "—"}
                          </td>
                          <td className="text-right pl-4">
                            {lag !== null ? lag : "—"}
                          </td>
                          <td className="pl-4 text-danger">{r.error ?? ""}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                <div className="mt-2 flex items-center justify-between border-t border-border pt-2">
                  <span className="text-muted">≈ total group lag after reset</span>
                  <LagBadge value={totalLag} />
                </div>
                <p className="mt-1 text-[10px] text-subtle-text">
                  {committed
                    ? "At commit time — live traffic may increase this."
                    : "Highlighted rows are the partitions you have selected to reset. At preview time — live traffic may increase this."}
                </p>
              </>
            );
          })()}
        </div>
        {err && (
          <Notice intent="danger">
            {err}
          </Notice>
        )}
      </div>
    </Modal>
  );
}
