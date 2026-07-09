import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  createGroup,
  type CreateGroupStrategy,
  type ResetOffsetResult,
} from "@/lib/api";
import { Button } from "@/components/button";
import { Input } from "@/components/Input";
import { Modal } from "@/components/Modal";
import { Notice } from "@/components/Notice";
import { localInputToMs, msToLocalInput } from "@/lib/datetime";

export function CreateGroupModal({
  cluster,
  topic,
  onClose,
  onCreated,
}: {
  cluster: string;
  topic: string;
  onClose: () => void;
  onCreated?: () => void;
}) {
  const qc = useQueryClient();
  const [groupId, setGroupId] = useState("");
  const [strategy, setStrategy] = useState<CreateGroupStrategy>("latest");
  const [offset, setOffset] = useState("0");
  const [timestampMs, setTimestampMs] = useState(String(Date.now() - 3600_000));
  const [preview, setPreview] = useState<ResetOffsetResult[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  function buildReq(dryRun: boolean) {
    return {
      group_id: groupId.trim(),
      topic,
      strategy,
      offset: strategy === "offset" ? Number(offset) : undefined,
      timestamp_ms: strategy === "timestamp" ? Number(timestampMs) : undefined,
      dry_run: dryRun,
    };
  }

  const mutation = useMutation({
    mutationFn: (dryRun: boolean) => createGroup(cluster, buildReq(dryRun)),
    onError: (e: unknown) => setErr((e as Error).message),
  });

  const groupIdValid = groupId.trim() !== "";
  const offsetValid =
    strategy !== "offset" ||
    (offset.trim() !== "" && Number.isInteger(Number(offset)));
  const timestampValid =
    strategy !== "timestamp" ||
    (timestampMs.trim() !== "" && Number.isFinite(Number(timestampMs)));
  const ready = groupIdValid && offsetValid && timestampValid;

  async function onPreview() {
    setErr(null);
    try {
      const res = await mutation.mutateAsync(true);
      setPreview(res.results);
    } catch {
      // error already surfaced via onError
    }
  }

  async function onCreate() {
    setErr(null);
    try {
      await mutation.mutateAsync(false);
    } catch {
      // error already surfaced via onError; keep the modal open
      return;
    }
    await qc.invalidateQueries({ queryKey: ["topic-consumers", cluster, topic] });
    onCreated?.();
    onClose();
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={
        <>
          Create consumer group for <span className="font-mono">{topic}</span>
        </>
      }
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={!ready || mutation.isPending}
            onClick={onPreview}
          >
            {mutation.isPending && mutation.variables === true
              ? "Working…"
              : "Preview"}
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={!ready || mutation.isPending}
            loading={mutation.isPending && mutation.variables === false}
            onClick={onCreate}
          >
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-4 text-sm">
        <div className="grid grid-cols-2 gap-4">
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Group name
            </span>
            <Input
              value={groupId}
              onChange={(e) => setGroupId(e.target.value)}
              className="mt-1 font-mono"
              placeholder="my-consumer-group"
            />
          </label>
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Strategy
            </span>
            <select
              value={strategy}
              onChange={(e) => {
                setStrategy(e.target.value as CreateGroupStrategy);
                setPreview(null);
              }}
              className="mt-1 h-9 w-full rounded-md border border-border bg-panel px-2 text-sm hover:border-border-hover"
            >
              <option value="earliest">earliest (log start)</option>
              <option value="latest">latest (log end)</option>
              <option value="offset">specific offset</option>
              <option value="timestamp">timestamp</option>
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
              onChange={(e) => {
                setOffset(e.target.value);
                setPreview(null);
              }}
              className="mt-1 font-mono"
            />
            <span className="mt-1 block text-xs text-muted">
              Applied to every partition of the topic.
            </span>
          </label>
        )}
        {strategy === "timestamp" && (
          <label className="block">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Timestamp (epoch ms)
            </span>
            <Input
              type="datetime-local"
              step="1"
              value={
                timestampMs.trim() !== "" && Number.isFinite(Number(timestampMs))
                  ? msToLocalInput(Number(timestampMs))
                  : ""
              }
              onChange={(e) => {
                const ms = localInputToMs(e.target.value);
                setTimestampMs(Number.isFinite(ms) ? String(ms) : "");
                setPreview(null);
              }}
              className="mt-1 max-w-[16rem] font-mono"
            />
          </label>
        )}

        <Notice intent="info">
          A group without an active consumer expires after{" "}
          <span className="font-mono">offsets.retention.minutes</span>.
        </Notice>

        {err && <Notice intent="danger">{err}</Notice>}

        {preview && (
          <div className="rounded-md border border-border bg-subtle p-2 text-xs">
            <div className="mb-1 font-semibold">Preview</div>
            {preview.length === 0 ? (
              <p className="text-subtle-text">No partitions to preview.</p>
            ) : (
              <table className="w-full font-mono">
                <thead className="text-[10px] uppercase tracking-wider text-subtle-text">
                  <tr>
                    <th className="text-left">partition</th>
                    <th className="text-right">new offset</th>
                    <th className="text-left pl-4">error</th>
                  </tr>
                </thead>
                <tbody>
                  {preview.map((r) => (
                    <tr key={r.partition}>
                      <td>p{r.partition}</td>
                      <td className="text-right">
                        {r.new_offset >= 0 ? r.new_offset : "—"}
                      </td>
                      <td className="pl-4 text-danger">{r.error ?? ""}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>
    </Modal>
  );
}
