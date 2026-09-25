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
//
// Large values (message.value_truncated) are a related but distinct case: the
// message list only ever holds the first 64 KB of a value, so replaying it
// as-is would silently write a truncated copy instead of the real record.
// When sourceCluster/sourceTopic are known, this modal fetches the full raw
// value first (the same endpoint "Download full value" uses) and replays
// that instead. If the fetch fails (e.g. the value exceeds the 15 MB raw-
// download cap), the user must explicitly opt in to replaying the truncated
// preview via a checkbox — never silently.
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchTopics, produceMessage, RawValueTooLargeError, type Message } from "@/lib/api";
import { produceEncodingFor, replayBlocker } from "@/lib/produce-encoding";
import { useCluster, type ClusterListItem } from "@/lib/use-cluster";
import { useFormatters } from "@/lib/use-formatters";
import { useMessageRawValue } from "@/lib/use-message-raw-value";
import { Modal } from "./Modal";
import { Button } from "./button";
import { ConfirmDialog } from "./confirm-dialog";
import { TopicCombobox } from "./topic-combobox";

interface ReplayModalProps {
  open: boolean;
  onClose: () => void;
  message: Message;
  /** Cluster/topic the message was consumed from — needed to recover the
   * full value when message.value_truncated. Omitting them falls back to
   * replaying the truncated preview after an explicit opt-in, same as a
   * failed fetch. */
  sourceCluster?: string;
  sourceTopic?: string;
}

type FullValueState =
  | { status: "idle" }
  | { status: "fetching" }
  | { status: "ready"; base64: string }
  | { status: "error"; message: string };

// Mirrors internal/server/clusters.go's maxProduceBodyBytes (15 MiB): the
// whole produce JSON body (key + value + headers) must fit under that cap
// once decompressed server-side — gzip (see lib/api.ts's maybeGzipBody)
// only shrinks bytes on the wire, not this ceiling. Base64 inflates raw
// bytes by ~4/3, and the request also carries the key, headers and JSON
// punctuation, so a full-value fetch that succeeds against the
// (independent, 15 MB) raw-download cap can still be too big to send to
// the produce endpoint. Reserve 512 KiB of headroom for that overhead and
// treat anything over the remainder as "too large to replay", the same way
// a fetch failure is handled — never attempt an upload we already know the
// server will reject with a generic body-too-large error.
const maxProduceBodyBytes = 15 * 1024 * 1024;
const produceValueHeadroomBytes = 512 * 1024;
const maxReplayValueBase64Chars = maxProduceBodyBytes - produceValueHeadroomBytes;

/**
 * Folds the raw-value query into the FullValueState the dialog renders.
 * Kept as a pure function so the precedence (no source > in flight > error >
 * too large > ready) is explicit and unit-testable instead of buried in JSX.
 */
function resolveFullValue(args: {
  needsFullValue: boolean;
  hasSource: boolean;
  isFetching: boolean;
  error: unknown;
  base64: string | undefined;
  sizeLabel: string;
}): FullValueState {
  if (!args.needsFullValue) return { status: "idle" };
  if (!args.hasSource) return { status: "error", message: "source cluster/topic unknown" };
  if (args.isFetching) return { status: "fetching" };
  if (args.error) {
    return {
      status: "error",
      message:
        args.error instanceof RawValueTooLargeError
          ? `value exceeds the download limit (${args.sizeLabel})`
          : args.error instanceof Error
            ? args.error.message
            : String(args.error),
    };
  }
  if (args.base64 === undefined) return { status: "fetching" };
  if (args.base64.length > maxReplayValueBase64Chars) {
    return {
      status: "error",
      message: `full value (${args.sizeLabel}) is too large to send to the produce API in one request`,
    };
  }
  return { status: "ready", base64: args.base64 };
}

export function ReplayModal({
  open,
  onClose,
  message,
  sourceCluster,
  sourceTopic,
}: ReplayModalProps) {
  const { clusters } = useCluster();
  const fmt = useFormatters();

  const [destCluster, setDestCluster] = useState<string>("");
  const [destTopic, setDestTopic] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<{ partition: number; offset: number } | null>(null);
  const [confirmProd, setConfirmProd] = useState(false);
  const [allowTruncated, setAllowTruncated] = useState(false);

  // Recover the untruncated value up front, whenever the modal opens on a
  // truncated message. The query key carries partition/offset, so switching
  // which row is being replayed never reuses a stale fetch, and re-opening
  // the modal on the same row serves from cache instead of downloading
  // megabytes again.
  const needsFullValue = open && !!message.value_truncated && !replayBlocker(message);
  const rawQuery = useMessageRawValue({
    cluster: sourceCluster ?? "",
    topic: sourceTopic ?? "",
    partition: message.partition,
    offset: message.offset,
    enabled: needsFullValue,
  });

  // Re-arm the "replay the truncated preview anyway" opt-in whenever the
  // modal targets a different record, so a previous acceptance cannot leak
  // into the next replay.
  // biome-ignore lint/correctness/useExhaustiveDependencies: deliberately keyed on which record is targeted, not on the whole message object (whose identity changes on every render).
  useEffect(() => {
    setAllowTruncated(false);
  }, [open, message.partition, message.offset]);

  const fullValue: FullValueState = resolveFullValue({
    needsFullValue,
    hasSource: !!sourceCluster && !!sourceTopic,
    isFetching: rawQuery.isFetching,
    error: rawQuery.error,
    base64: rawQuery.data,
    sizeLabel: fmt.bytes(message.value_size_bytes ?? 0),
  });

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
  // When the full value was recovered, replay that instead of the 64 KB
  // preview produceEncodingFor would otherwise send.
  const valuePayload =
    fullValue.status === "ready"
      ? { value: fullValue.base64, encoding: "base64" as const }
      : produceEncodingFor(message.value, message.value_b64, message.value_encoding);

  // Gates the Replay button while a truncated value's full form is still
  // being fetched, or until the user explicitly accepts a truncated replay.
  const truncatedNotResolved =
    message.value_truncated &&
    fullValue.status !== "ready" &&
    (fullValue.status !== "error" || !allowTruncated);

  const doReplay = async (confirmedProd: boolean) => {
    if (!effectiveCluster || !destTopic.trim()) return;
    if (blocker || !keyPayload || !valuePayload || truncatedNotResolved) return;
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
  const canReplay =
    !!effectiveCluster && !!destTopic.trim() && !busy && !blocker && !truncatedNotResolved;

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
              {result ? "Close" : "Cancel"}
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
            Reproduces this message (key&thinsp;+&thinsp;value&thinsp;+&thinsp;headers) to the
            selected destination cluster and topic.
          </p>

          {blocker && (
            <div className="rounded-md border border-danger/30 bg-danger-subtle p-2 text-xs text-danger">
              <div className="font-semibold">Replay not possible</div>
              <p className="mt-0.5">{blocker.reason}</p>
            </div>
          )}

          {!blocker && message.value_truncated && fullValue.status === "fetching" && (
            <div className="rounded-md border border-border bg-panel p-2 text-xs text-muted">
              Fetching the full value ({fmt.bytes(message.value_size_bytes ?? 0)}) so it can be
              replayed byte-for-byte…
            </div>
          )}

          {!blocker && message.value_truncated && fullValue.status === "ready" && (
            <div className="rounded-md border border-success/30 bg-success-subtle p-2 text-xs text-success">
              Full value ({fmt.bytes(message.value_size_bytes ?? 0)}) loaded — replay will send the
              complete record, not just the 64&nbsp;KB preview.
            </div>
          )}

          {!blocker && message.value_truncated && fullValue.status === "error" && (
            <div className="rounded-md border border-warning/30 bg-warning-subtle p-2 text-xs text-warning">
              <div className="font-semibold">Only a 64&nbsp;KB preview is available</div>
              <p className="mt-0.5">
                Could not recover the full value ({fmt.bytes(message.value_size_bytes ?? 0)} total):{" "}
                {fullValue.message}.
              </p>
              <label className="mt-2 flex cursor-pointer items-center gap-2">
                <input
                  type="checkbox"
                  checked={allowTruncated}
                  onChange={(e) => setAllowTruncated(e.target.checked)}
                  className="h-3.5 w-3.5 accent-accent"
                />
                Replay the truncated 64&nbsp;KB preview anyway (not byte-for-byte)
              </label>
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
            <TopicCombobox
              value={destTopic}
              onChange={(v) => {
                setDestTopic(v);
                setResult(null);
                setError(null);
              }}
              topics={(topicsQuery.data ?? []).map((t) => t.name)}
              placeholder="topic-name"
            />
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
