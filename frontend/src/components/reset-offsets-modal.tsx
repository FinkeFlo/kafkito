import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  resetGroupOffsets,
  type GroupDetail,
  type ResetStrategy,
} from "@/lib/api";
import { Button } from "@/components/button";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Input } from "@/components/Input";
import { Modal } from "@/components/Modal";
import { Notice } from "@/components/Notice";

export function ResetOffsetsModal({
  cluster,
  detail,
  onClose,
}: {
  cluster: string;
  detail: GroupDetail;
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
  const [partSel, setPartSel] = useState<Record<number, boolean>>({});
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<
    | {
        partition: number;
        old_offset: number;
        new_offset: number;
        error?: string;
      }[]
    | null
  >(null);
  const [dryResult, setDryResult] = useState<typeof result>(null);
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

  const buildBody = (dry_run: boolean) => ({
    topic,
    partitions: selectedParts.length > 0 ? selectedParts : undefined,
    strategy,
    offset: strategy === "offset" ? Number(offset) : undefined,
    timestamp_ms: strategy === "timestamp" ? Number(timestampMs) : undefined,
    shift: strategy === "shift-by" ? Number(shift) : undefined,
    dry_run,
  });

  const dryMut = useMutation({
    mutationFn: () => resetGroupOffsets(cluster, detail.group_id, buildBody(true)),
    onSuccess: (r) => setDryResult(r.results),
    onError: (e: Error) => setErr(e.message),
  });
  const commitMut = useMutation({
    mutationFn: () => resetGroupOffsets(cluster, detail.group_id, buildBody(false)),
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
            variant="secondary"
            size="sm"
            disabled={dryMut.isPending}
            onClick={() => {
              setErr(null);
              setResult(null);
              dryMut.mutate();
            }}
          >
            {dryMut.isPending ? "…" : "Preview"}
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={commitMut.isPending || selectedParts.length === 0}
            onClick={() => setCommitOpen(true)}
          >
            {commitMut.isPending ? "Committing…" : "Commit reset"}
          </Button>
          <ConfirmDialog
            open={commitOpen}
            onOpenChange={setCommitOpen}
            variant="primary"
            title="Commit new offsets?"
            description={`This will overwrite committed offsets for group "${detail.group_id}" on topic "${topic}". Partitions: ${selectedParts.join(",")} (${selectedParts.length} of ${topicParts.length}).`}
            confirmPhrase={detail.group_id}
            confirmLabel="Commit reset"
            onConfirm={() => {
              setErr(null);
              setDryResult(null);
              commitMut.mutate();
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
                setPartSel({});
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
              <option value="timestamp">timestamp (ms)</option>
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
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Timestamp (epoch ms)
            </span>
            <Input
              value={timestampMs}
              onChange={(e) => setTimestampMs(e.target.value)}
              className="mt-1 font-mono"
            />
          </label>
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

        {(dryResult || result) && (
          <div className="rounded-md border border-border bg-subtle p-2 text-xs">
            <div className="mb-1 font-semibold">
              {result ? "Committed" : "Preview (dry-run)"}
            </div>
            <table className="w-full font-mono">
              <thead className="text-[10px] uppercase tracking-wider text-subtle-text">
                <tr>
                  <th className="text-left">partition</th>
                  <th className="text-right">old</th>
                  <th className="text-right">→ new</th>
                  <th className="text-left pl-4">error</th>
                </tr>
              </thead>
              <tbody>
                {(result ?? dryResult)!.map((r) => (
                  <tr key={r.partition}>
                    <td>p{r.partition}</td>
                    <td className="text-right text-muted">
                      {r.old_offset >= 0 ? r.old_offset : "—"}
                    </td>
                    <td className="text-right">
                      {r.new_offset >= 0 ? r.new_offset : "—"}
                    </td>
                    <td className="pl-4 text-danger">{r.error ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {err && (
          <Notice intent="danger">
            {err}
          </Notice>
        )}
      </div>
    </Modal>
  );
}
