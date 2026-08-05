// ReplayModal — lets the user replay a single captured message to an arbitrary
// cluster / topic (potentially different from where it was consumed).
//
// Key, value and all headers are reproduced byte-for-byte. The produce
// encoding is derived per field by `produceEncodingFor` (shared fidelity rules
// with internal/server/topic_copy.go), because the rendering the consumer
// returns is not always the payload: "binary" fields carry only a truncated
// hex preview and must be sent from their base64 form, and "empty" fields need
// the "empty" encoding so a zero-length payload isn't turned into a tombstone.
// Non-UTF-8 header values are sent via `headers_b64` for the same reason.
// Messages whose original bytes are unrecoverable (Schema-Registry-decoded or
// masked) are refused up front — see `replayBlocker`.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  fetchTopics,
  produceMessage,
  type Message,
} from "@/lib/api";
import { produceEncodingFor, replayBlocker } from "@/lib/produce-encoding";
import { useCluster, type ClusterListItem } from "@/lib/use-cluster";
import { Modal } from "./Modal";
import { Button } from "./button";
import { ConfirmDialog } from "./confirm-dialog";

interface ReplayModalProps {
  open: boolean;
  onClose: () => void;
  message: Message;
}

export function ReplayModal({ open, onClose, message }: ReplayModalProps) {
  const { clusters } = useCluster();

  const [destCluster, setDestCluster] = useState<string>("");
  const [destTopic, setDestTopic] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<{ partition: number; offset: number } | null>(null);
  const [confirmProd, setConfirmProd] = useState(false);

  // Determine the effective cluster selection (default to first cluster).
  const clusterList: ClusterListItem[] = clusters ?? [];
  const effectiveCluster = destCluster || clusterList[0]?.name || "";

  // Load topics for the selected cluster (lazy).
  const topicsQuery = useQuery({
    queryKey: ["topics", effectiveCluster],
    queryFn: () => fetchTopics(effectiveCluster),
    enabled: open && !!effectiveCluster,
    staleTime: 30_000,
  });

  const isProdDest = !!clusterList.find((c) => c.name === effectiveCluster)?.is_prod;

  // Fidelity check. Non-null means the original bytes cannot be reproduced, so
  // nothing is sent at all; `keyPayload`/`valuePayload` are then unusable.
  const blocker = replayBlocker(message);
  const keyPayload = produceEncodingFor(message.key, message.key_b64, message.key_encoding);
  const valuePayload = produceEncodingFor(message.value, message.value_b64, message.value_encoding);

  const doReplay = async (confirmedProd: boolean) => {
    if (!effectiveCluster || !destTopic.trim()) return;
    if (blocker || !keyPayload || !valuePayload) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const res = await produceMessage(
        effectiveCluster,
        destTopic.trim(),
        {
          key: keyPayload.value,
          value: valuePayload.value,
          key_encoding: keyPayload.encoding,
          value_encoding: valuePayload.encoding,
          headers: message.headers,
          headers_b64: message.headers_b64,
        },
        confirmedProd,
      );
      setResult({ partition: res.partition, offset: res.offset });
    } catch (e: unknown) {
      const msg = (e as Error).message ?? String(e);
      // 428 = production confirmation required
      if (msg.includes("428")) {
        setConfirmProd(true);
        return;
      }
      setError(msg);
    } finally {
      setBusy(false);
    }
  };

  const handleReplay = () => {
    if (blocker) return;
    if (isProdDest) {
      setConfirmProd(true);
    } else {
      void doReplay(false);
    }
  };

  const handleClose = () => {
    if (busy) return;
    setError(null);
    setResult(null);
    setConfirmProd(false);
    onClose();
  };

  const labelCls = "text-[11px] font-semibold uppercase tracking-wider text-muted";
  const canReplay = !!effectiveCluster && !!destTopic.trim() && !busy && !blocker;

  return (
    <>
      <Modal
        open={open}
        onClose={handleClose}
        title="Replay message to…"
        size="md"
        actions={
          <>
            <Button variant="ghost" size="sm" onClick={handleClose} disabled={busy}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={handleReplay}
              loading={busy}
              disabled={!canReplay}
            >
              {busy ? "Replaying…" : "Replay"}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <p className="text-xs text-muted">
            Reproduces this message (key&thinsp;+&thinsp;value&thinsp;+&thinsp;headers) to
            the selected destination cluster and topic.
          </p>

          {blocker && (
            <div className="rounded-md border border-danger/30 bg-danger-subtle p-2 text-xs text-danger">
              <div className="font-semibold">Replay not possible</div>
              <p className="mt-0.5">{blocker.reason}</p>
            </div>
          )}

          <div>
            <label className={`mb-1 block ${labelCls}`}>Destination cluster</label>
            <select
              value={effectiveCluster}
              onChange={(e) => {
                setDestCluster(e.target.value);
                setDestTopic("");
                setResult(null);
                setError(null);
              }}
              className="w-full rounded-md border border-border bg-panel px-2 py-1.5 text-sm"
            >
              {clusterList.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name}
                  {c.is_prod ? " ⚠ prod" : ""}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label className={`mb-1 block ${labelCls}`}>Destination topic</label>
            <input
              list="replay-topics"
              value={destTopic}
              onChange={(e) => {
                setDestTopic(e.target.value);
                setResult(null);
                setError(null);
              }}
              placeholder="topic-name"
              className="w-full rounded-md border border-border bg-panel px-3 py-1.5 text-sm font-mono"
            />
            {topicsQuery.data && topicsQuery.data.length > 0 && (
              <datalist id="replay-topics">
                {topicsQuery.data.map((t) => (
                  <option key={t.name} value={t.name} />
                ))}
              </datalist>
            )}
          </div>

          {error && (
            <div className="rounded-md border border-danger/30 bg-danger-subtle p-2 text-xs text-danger">
              {error}
            </div>
          )}
          {result && (
            <div className="rounded-md border border-success/30 bg-success-subtle p-2 text-xs text-success">
              ✓ Replayed to partition {result.partition}, offset {result.offset}
            </div>
          )}
        </div>
      </Modal>

      <ConfirmDialog
        open={confirmProd}
        onOpenChange={(v) => {
          setConfirmProd(v);
        }}
        title="Production cluster warning"
        description={`You are replaying a message to the production cluster "${effectiveCluster}". This can impact live systems.`}
        confirmLabel="Replay anyway"
        cancelLabel="Cancel"
        variant="danger"
        onConfirm={() => doReplay(true)}
      />
    </>
  );
}
