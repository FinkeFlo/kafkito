import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ArrowLeft, Check, Copy } from "lucide-react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  resetGroupOffsets,
  type GroupDetail,
  type ResetOffsetResult,
  type ResetStrategy,
} from "@/lib/api";
import { Button } from "@/components/ui/Button";
import { useFieldError } from "@/components/ui/FieldError";
import { IconButton } from "@/components/ui/IconButton";
import { Input } from "@/components/ui/Input";
import { Modal } from "@/components/ui/Modal";
import { Notice } from "@/components/ui/Notice";
import { Timestamp } from "@/components/ui/Timestamp";
import { LagBadge } from "@/features/groups/LagBadge";
import { localInputToMs, msToLocalInput } from "@/lib/datetime";
import { groupQueries } from "@/lib/queries/groups";

function partitionsForTopic(detail: GroupDetail, topic: string): number[] {
  return detail.offsets.filter((o) => o.topic === topic).map((o) => o.partition);
}

function selectAll(parts: number[]): Record<number, boolean> {
  return Object.fromEntries(parts.map((p) => [p, true]));
}

// Lag after the operation for a partition: moved partitions sit at the new
// offset, everything else keeps its current committed offset.
function lagAfter(r: ResetOffsetResult, moved: boolean): number | null {
  const base = moved ? r.new_offset : r.old_offset;
  if (r.error || base < 0 || r.end_offset < 0) return null;
  return Math.max(0, r.end_offset - base);
}

function sumKnown(values: (number | null)[]): number | null {
  const known = values.filter((l): l is number => l !== null);
  return known.length > 0 ? known.reduce((a, b) => a + b, 0) : null;
}

type Step = "configure" | "review" | "done";

function SummaryRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[7rem_minmax(0,1fr)] items-baseline gap-3">
      <dt className="text-xs font-semibold uppercase tracking-wider text-muted">{label}</dt>
      <dd className="min-w-0 text-text">{children}</dd>
    </div>
  );
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
  const [step, setStep] = useState<Step>("configure");
  const [typed, setTyped] = useState("");
  const [copied, setCopied] = useState(false);
  const phraseRef = useRef<HTMLInputElement | null>(null);
  const reviewTriggerRef = useRef<HTMLButtonElement | null>(null);
  const doneRef = useRef<HTMLButtonElement | null>(null);

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
    timestampMs.trim() !== "" && Number.isFinite(timestampNum) && timestampNum > 0;
  const localValue = timestampValid ? msToLocalInput(timestampNum) : "";

  const offsetValid =
    strategy !== "offset" || (offset.trim() !== "" && Number.isFinite(Number(offset)));
  const shiftValid =
    strategy !== "shift-by" || (shift.trim() !== "" && Number.isFinite(Number(shift)));
  const strategyReady =
    (strategy === "timestamp" ? timestampValid : true) && offsetValid && shiftValid;
  const offsetField = useFieldError(offsetValid ? null : "Enter a numeric offset.");
  const shiftField = useFieldError(shiftValid ? null : "Enter a numeric shift.");
  const timestampField = useFieldError(timestampValid ? null : "Pick a valid date and time.");

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
    ...groupQueries.resetPreview(cluster, detail.group_id, debouncedBody, isProd),
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
      qc.invalidateQueries({ queryKey: groupQueries.detail(cluster, detail.group_id).queryKey });
      qc.invalidateQueries({ queryKey: groupQueries.list(cluster).queryKey });
    },
    onError: (e: Error) => setErr(e.message),
  });

  const phraseOk = typed === detail.group_id;
  const canReview = selectedParts.length > 0 && strategyReady;

  // Each step swaps the body, so focus would fall back to <body>; move it to
  // the control the user acts on next. The first render is left to <Modal>.
  const prevStep = useRef(step);
  useEffect(() => {
    if (prevStep.current === step) return;
    prevStep.current = step;
    if (step === "review") phraseRef.current?.focus();
    else if (step === "done") doneRef.current?.focus();
    else reviewTriggerRef.current?.focus();
  }, [step]);

  const goBack = () => {
    if (commitMut.isPending) return;
    setErr(null);
    setTyped("");
    setStep("configure");
  };

  const commit = async () => {
    if (!phraseOk || commitMut.isPending) return;
    setErr(null);
    try {
      await commitMut.mutateAsync();
      setStep("done");
    } catch {
      // onError has already surfaced the message inline.
    }
  };

  const copyPhrase = async () => {
    try {
      await navigator.clipboard.writeText(detail.group_id);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      // Clipboard may be blocked; typing still works.
    }
  };

  // Only trust the preview when it was computed for the current inputs;
  // keepPreviousData may still hold rows for an earlier configuration.
  const previewCurrent =
    debouncedBody === previewBody && !previewQuery.isFetching && !previewQuery.isPlaceholderData;
  const projectedLag =
    previewRows && previewCurrent
      ? sumKnown(previewRows.map((r) => lagAfter(r, !!partSel[r.partition])))
      : null;

  const strategySummary: ReactNode = (() => {
    switch (strategy) {
      case "earliest":
        return "Earliest (log start)";
      case "latest":
        return "Latest (log end)";
      case "offset":
        return (
          <>
            Offset <span className="font-mono tabular-nums">{offset}</span>
          </>
        );
      case "timestamp":
        return (
          <>
            Timestamp · <Timestamp value={timestampNum} zone="utc" /> (UTC)
          </>
        );
      case "shift-by":
        return (
          <>
            Shift by <span className="font-mono tabular-nums">{shift}</span> records
          </>
        );
    }
  })();

  const renderLagPanel = () => (
    <div className="rounded-md border border-border bg-subtle p-2 text-xs">
      <div className="mb-1 flex items-center justify-between">
        <span className="font-semibold">{result ? "Committed" : "Lag preview"}</span>
        {!result && previewQuery.isFetching && (
          <span className="text-[10px] text-subtle-text">updating…</span>
        )}
      </div>
      {(() => {
        const committed = !!result;
        const rows = result ?? previewRows;

        if (!committed && !strategyReady) {
          return (
            <p className="text-subtle-text">Enter a valid value to preview the projected lag.</p>
          );
        }
        if (!committed && previewQuery.isError) {
          return <p className="text-danger">{(previewQuery.error as Error).message}</p>;
        }
        if (!rows) {
          return <p className="text-subtle-text">Calculating preview…</p>;
        }
        if (rows.length === 0) {
          return <p className="text-subtle-text">No partitions to preview.</p>;
        }

        const isMoved = (r: ResetOffsetResult) => committed || !!partSel[r.partition];
        const totalLag = sumKnown(rows.map((r) => lagAfter(r, isMoved(r))));

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
                  const active = isMoved(r);
                  const lag = lagAfter(r, active);
                  return (
                    <tr key={r.partition} className={active ? "" : "text-subtle-text"}>
                      <td className={active ? "text-accent" : undefined}>
                        <span className="inline-flex items-center gap-1">
                          {active && !committed ? (
                            <Check role="img" aria-label="selected" className="h-3 w-3" />
                          ) : (
                            <span aria-hidden="true" className="inline-block w-3" />
                          )}
                          p{r.partition}
                        </span>
                      </td>
                      <td className="text-right text-muted">
                        {r.old_offset >= 0 ? r.old_offset : "—"}
                      </td>
                      <td className="text-right">{r.new_offset >= 0 ? r.new_offset : "—"}</td>
                      <td className="text-right pl-4 text-muted">
                        {r.end_offset >= 0 ? r.end_offset : "—"}
                      </td>
                      <td className="text-right pl-4">{lag !== null ? lag : "—"}</td>
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
                : "Rows marked with a check are the partitions you have selected to reset. At preview time — live traffic may increase this."}
            </p>
          </>
        );
      })()}
    </div>
  );

  const actions =
    step === "configure" ? (
      <>
        <Button variant="ghost" size="sm" onClick={onClose}>
          Close
        </Button>
        <Button
          ref={reviewTriggerRef}
          variant="primary"
          size="sm"
          disabled={!canReview}
          aria-describedby={canReview ? undefined : "reset-offsets-review-reason"}
          onClick={() => setStep("review")}
        >
          Review reset
        </Button>
        {!canReview && (
          <span id="reset-offsets-review-reason" className="sr-only">
            Select at least one partition and enter a valid value to review.
          </span>
        )}
      </>
    ) : step === "review" ? (
      <>
        <Button
          variant="ghost"
          size="sm"
          className="mr-auto"
          onClick={goBack}
          disabled={commitMut.isPending}
        >
          <ArrowLeft aria-hidden="true" className="h-4 w-4" />
          Back
        </Button>
        <Button
          type="submit"
          variant="danger"
          size="sm"
          disabled={!phraseOk}
          aria-describedby={phraseOk ? undefined : "reset-offsets-commit-reason"}
          loading={commitMut.isPending}
        >
          Commit reset
        </Button>
        {!phraseOk && (
          <span id="reset-offsets-commit-reason" className="sr-only">
            Type the group name exactly to enable.
          </span>
        )}
      </>
    ) : (
      <Button
        ref={doneRef}
        variant="primary"
        size="sm"
        aria-describedby="reset-offsets-done"
        onClick={onClose}
      >
        Done
      </Button>
    );

  return (
    <Modal
      open
      // Escape and backdrop on the review step step back instead of
      // discarding the whole configuration.
      onClose={step === "review" ? goBack : onClose}
      onSubmit={step === "review" ? () => void commit() : undefined}
      size="lg"
      title={
        <>
          Reset offsets — <span className="font-mono">{detail.group_id}</span>
        </>
      }
      actions={actions}
    >
      {step === "review" ? (
        <div className="space-y-4 text-sm">
          {isProd && (
            <Notice intent="warning" title="Production cluster">
              Committed offsets are overwritten immediately.
            </Notice>
          )}
          <dl className="space-y-2">
            <SummaryRow label="Group">
              <span className="break-all font-mono">{detail.group_id}</span>
            </SummaryRow>
            <SummaryRow label="Topic">
              <span className="break-all font-mono">{topic}</span>
            </SummaryRow>
            <SummaryRow label="Strategy">{strategySummary}</SummaryRow>
            <SummaryRow label="Partitions">
              <span className="font-mono tabular-nums">
                {selectedParts.map((p) => `p${p}`).join(", ")}
              </span>
              <span className="text-muted">
                {" · "}
                <span className="font-mono tabular-nums">{selectedParts.length}</span> of{" "}
                <span className="font-mono tabular-nums">{topicParts.length}</span>
              </span>
            </SummaryRow>
            <SummaryRow label="Lag after">
              <LagBadge value={projectedLag} />
            </SummaryRow>
          </dl>
          <p className="text-muted">
            This overwrites the committed offsets of the selected partitions.
          </p>
          <div className="space-y-1.5">
            <label htmlFor="reset-offsets-confirm" className="block text-xs font-medium text-muted">
              Type the group name to confirm
            </label>
            <div className="flex items-start gap-2">
              <code className="min-w-0 flex-1 break-all rounded-md border border-border bg-subtle px-3 py-1.5 font-mono text-sm text-text">
                {detail.group_id}
              </code>
              <IconButton
                size="sm"
                variant="secondary"
                aria-label={copied ? "Copied" : "Copy group name"}
                onClick={copyPhrase}
                icon={
                  copied ? (
                    <Check aria-hidden="true" className="h-4 w-4 text-success" />
                  ) : (
                    <Copy aria-hidden="true" className="h-4 w-4" />
                  )
                }
              />
            </div>
            <Input
              ref={phraseRef}
              id="reset-offsets-confirm"
              name="confirm-phrase"
              autoComplete="off"
              spellCheck={false}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              className="font-mono"
            />
          </div>
          {err && <Notice intent="danger">{err}</Notice>}
        </div>
      ) : step === "done" ? (
        <div className="space-y-4 text-sm">
          <div id="reset-offsets-done">
            <Notice intent="success">
              Offsets committed for{" "}
              <span className="font-mono tabular-nums">{result?.length ?? 0}</span>{" "}
              {result?.length === 1 ? "partition" : "partitions"}.
            </Notice>
          </div>
          {renderLagPanel()}
        </div>
      ) : (
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
            <div>
              <label className="block">
                <span className="text-xs font-semibold uppercase tracking-wider text-muted">
                  Offset
                </span>
                <Input
                  value={offset}
                  onChange={(e) => setOffset(e.target.value)}
                  className="mt-1 font-mono"
                  {...offsetField.controlProps}
                />
                <span className="mt-1 block text-xs text-muted">
                  Applied to every selected partition. Clamped to [start, end].
                </span>
              </label>
              {offsetField.message}
            </div>
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
                    {...timestampField.controlProps}
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
                timestampField.message
              )}
            </div>
          )}
          {strategy === "shift-by" && (
            <div>
              <label className="block">
                <span className="text-xs font-semibold uppercase tracking-wider text-muted">
                  Shift (records, negative allowed)
                </span>
                <Input
                  value={shift}
                  onChange={(e) => setShift(e.target.value)}
                  className="mt-1 font-mono"
                  {...shiftField.controlProps}
                />
              </label>
              {shiftField.message}
            </div>
          )}
          <div>
            <div className="mb-1 flex items-center justify-between text-xs">
              <span className="font-semibold uppercase tracking-wider text-muted">Partitions</span>
              <div className="flex gap-2 text-xs">
                <button
                  type="button"
                  onClick={() => setPartSel(Object.fromEntries(topicParts.map((p) => [p, true])))}
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
                      onChange={(e) => setPartSel((s) => ({ ...s, [p]: e.target.checked }))}
                      className="sr-only"
                    />
                    {partSel[p] && <Check aria-hidden="true" className="h-3 w-3" strokeWidth={3} />}
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

          {renderLagPanel()}
        </div>
      )}
    </Modal>
  );
}
