// BulkCopyPanel — inline panel for copying a range of messages from the
// current topic to another cluster / topic.  Streams progress via SSE from
// the server-side POST /copy endpoint so the copy runs entirely server-side
// and is not limited by browser timeouts or memory.
import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Square, Play } from "lucide-react";
import {
  copyMessages,
  fetchTopics,
  type CopyProgressEvent,
  type CopyRequest,
} from "@/lib/api";
import { useCluster, type ClusterListItem } from "@/lib/use-cluster";
import { Button } from "./button";
import { Input } from "./Input";
import { ConfirmDialog } from "./confirm-dialog";
import { getPrivateClusterByName, toBackendClusterConfig } from "@/lib/private-clusters";

interface BulkCopyPanelProps {
  srcCluster: string;
  srcTopic: string;
  partitions: number[];
}

/**
 * Why the server leaves records out. It skips anything it cannot reproduce
 * byte-for-byte, and it cannot tell us per record which of the two reasons
 * applied — so the label stays cause-agnostic and the detail lives here.
 */
const SKIPPED_TOOLTIP =
  "Records that cannot be reproduced byte-for-byte are left out: payloads decoded " +
  "through the Schema Registry (the original wire-format bytes are gone) and values " +
  "redacted by this cluster's data masking rules.";

/**
 * The "To" bound is exclusive, and an empty "To" is pinned to the moment the
 * job starts — otherwise a copy of a live topic would tail it forever.
 */
const TIME_RANGE_TOOLTIP =
  "An empty From starts at the oldest available record. An empty To stops at the " +
  "moment the copy starts, so records produced after that are not included. To is " +
  "exclusive: a record with exactly that timestamp is not copied.";

const PRESET_DURATIONS_MS: Record<string, number> = {
  "30m": 30 * 60_000,
  "1h": 60 * 60_000,
  "6h": 6 * 60 * 60_000,
  "24h": 24 * 60 * 60_000,
  "7d": 7 * 24 * 60 * 60_000,
};

export function BulkCopyPanel({ srcCluster, srcTopic, partitions }: BulkCopyPanelProps) {
  const { clusters } = useCluster();
  const clusterList: ClusterListItem[] = clusters ?? [];

  const [destCluster, setDestCluster] = useState<string>("");
  const [destTopic, setDestTopic] = useState<string>("");
  const [partition, setPartition] = useState<string>("all");
  const [fromTs, setFromTs] = useState<string>("");
  const [toTs, setToTs] = useState<string>("");
  const [limit, setLimit] = useState<string>("");
  const [preservePartition, setPreservePartition] = useState(false);

  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<CopyProgressEvent | null>(null);
  const [stopped, setStopped] = useState(false);
  const [confirmProd, setConfirmProd] = useState(false);

  const abortRef = useRef<(() => void) | null>(null);

  const effectiveCluster = destCluster || clusterList[0]?.name || "";
  const isProdDest = !!clusterList.find((c) => c.name === effectiveCluster)?.is_prod;

  const topicsQuery = useQuery({
    queryKey: ["topics", effectiveCluster],
    queryFn: () => fetchTopics(effectiveCluster),
    enabled: !!effectiveCluster,
    staleTime: 30_000,
  });

  const applyPreset = (key: string) => {
    const now = new Date();
    const ms = PRESET_DURATIONS_MS[key];
    if (!ms) return;
    const from = new Date(now.getTime() - ms);
    setFromTs(toLocalDatetimeString(from));
    setToTs(toLocalDatetimeString(now));
  };

  const startCopy = (confirmedProd: boolean) => {
    if (!effectiveCluster || !destTopic.trim() || running) return;
    setRunning(true);
    setProgress(null);
    setStopped(false);

    const req: CopyRequest = {
      dest_topic: destTopic.trim(),
      preserve_partition: preservePartition,
    };

    // Destination cluster: if it's a private cluster, embed config in body.
    const priv = getPrivateClusterByName(effectiveCluster);
    if (priv) {
      req.dest_cluster_config = toBackendClusterConfig(priv);
    } else {
      req.dest_cluster = effectiveCluster;
    }

    if (partition !== "all") {
      req.partition = Number.parseInt(partition, 10);
    }
    if (fromTs) {
      req.from_ts_ms = new Date(fromTs).getTime();
    }
    if (toTs) {
      req.to_ts_ms = new Date(toTs).getTime();
    }
    if (limit.trim()) {
      const n = Number.parseInt(limit, 10);
      if (n > 0) req.limit = n;
    }

    abortRef.current = copyMessages(
      srcCluster,
      srcTopic,
      req,
      confirmedProd,
      (ev) => {
        setProgress(ev);
        if (ev.done) {
          setRunning(false);
          abortRef.current = null;
        }
      },
    );
  };

  const handleStart = () => {
    if (isProdDest) {
      setConfirmProd(true);
    } else {
      startCopy(false);
    }
  };

  const handleStop = () => {
    abortRef.current?.();
    abortRef.current = null;
    setRunning(false);
    // Aborting only closes our stream. The server-side job finds out when its
    // next progress write fails, so a few more records can still land.
    setStopped(true);
  };

  const canStart = !!effectiveCluster && !!destTopic.trim() && !running;
  const labelCls = "text-[11px] font-semibold uppercase tracking-wider text-muted";

  const copied = progress?.copied ?? 0;
  const skipped = progress?.skipped ?? 0;
  const error = progress?.error;
  // The server refuses new jobs once too many copies run at once; api.ts hands
  // that to us as an `HTTP 429: <detail>` error string.
  const rateLimited = !!error && error.startsWith("HTTP 429");

  return (
    <>
      <div className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          {/* Destination cluster */}
          <div>
            <label className={`mb-1 block ${labelCls}`}>Destination cluster</label>
            <select
              value={effectiveCluster}
              onChange={(e) => {
                setDestCluster(e.target.value);
                setDestTopic("");
                setProgress(null);
                setStopped(false);
              }}
              disabled={running}
              className="w-full rounded-md border border-border bg-panel px-2 py-1.5 text-sm disabled:opacity-50"
            >
              {clusterList.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name}
                  {c.is_prod ? " ⚠ prod" : ""}
                </option>
              ))}
            </select>
          </div>

          {/* Destination topic */}
          <div>
            <label className={`mb-1 block ${labelCls}`}>Destination topic</label>
            <input
              list="bulk-copy-topics"
              value={destTopic}
              onChange={(e) => {
                setDestTopic(e.target.value);
                setProgress(null);
                setStopped(false);
              }}
              placeholder="topic-name"
              disabled={running}
              className="w-full rounded-md border border-border bg-panel px-3 py-1.5 text-sm font-mono disabled:opacity-50"
            />
            {topicsQuery.data && topicsQuery.data.length > 0 && (
              <datalist id="bulk-copy-topics">
                {topicsQuery.data.map((t) => (
                  <option key={t.name} value={t.name} />
                ))}
              </datalist>
            )}
          </div>
        </div>

        {/* Time range */}
        <div>
          <div className="mb-1 flex items-center gap-2">
            <label className={labelCls}>Time range</label>
            <span className="text-[10px] text-subtle-text" title={TIME_RANGE_TOOLTIP}>
              (empty From = oldest record; empty To = the moment the copy starts)
            </span>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {Object.keys(PRESET_DURATIONS_MS).map((key) => (
              <button
                key={key}
                type="button"
                onClick={() => applyPreset(key)}
                disabled={running}
                className="rounded border border-border px-2 py-0.5 text-[11px] hover:border-border-strong disabled:opacity-50"
              >
                Last {key}
              </button>
            ))}
            <button
              type="button"
              onClick={() => { setFromTs(""); setToTs(""); }}
              disabled={running}
              className="rounded border border-border px-2 py-0.5 text-[11px] hover:border-border-strong disabled:opacity-50"
            >
              Clear
            </button>
          </div>
          <div className="mt-2 grid gap-2 sm:grid-cols-2">
            <div>
              <label className="mb-0.5 block text-[10px] text-muted">From</label>
              <Input
                type="datetime-local"
                value={fromTs}
                onChange={(e) => setFromTs(e.target.value)}
                disabled={running}
                className="w-full text-sm"
              />
            </div>
            <div>
              <label className="mb-0.5 block text-[10px] text-muted">To</label>
              <Input
                type="datetime-local"
                value={toTs}
                onChange={(e) => setToTs(e.target.value)}
                disabled={running}
                className="w-full text-sm"
              />
            </div>
          </div>
        </div>

        {/* Limit + Partition row */}
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label className={`mb-1 block ${labelCls}`}>Max messages</label>
            <Input
              type="number"
              min={1}
              value={limit}
              onChange={(e) => setLimit(e.target.value)}
              placeholder="no limit"
              disabled={running}
              className="w-full text-sm"
            />
          </div>
          <div>
            <label className={`mb-1 block ${labelCls}`}>Source partition</label>
            <select
              value={partition}
              onChange={(e) => setPartition(e.target.value)}
              disabled={running}
              className="w-full rounded-md border border-border bg-panel px-2 py-1.5 text-sm disabled:opacity-50"
            >
              <option value="all">All partitions</option>
              {partitions.map((p) => (
                <option key={p} value={p}>
                  Partition {p}
                </option>
              ))}
            </select>
          </div>
        </div>

        {/* Preserve partition toggle */}
        <label className="flex cursor-pointer items-center gap-2 text-xs text-text">
          <input
            type="checkbox"
            checked={preservePartition}
            onChange={(e) => setPreservePartition(e.target.checked)}
            disabled={running}
            className="rounded"
          />
          Preserve source partition on destination
        </label>

        {/* Progress */}
        {(progress || stopped) && (
          <div
            className={`rounded-md border p-2 text-xs ${
              error
                ? "border-danger/30 bg-danger-subtle text-danger"
                : stopped
                  ? "border-warning/30 bg-warning-subtle text-warning"
                  : progress?.done
                    ? "border-success/30 bg-success-subtle text-success"
                    : "border-border bg-panel text-text"
            }`}
          >
            {error ? (
              rateLimited ? (
                // Keep the server's own wording reachable via the tooltip.
                <span title={error}>
                  Too many copies are running right now — try again in a moment.
                </span>
              ) : (
                `Error: ${error}`
              )
            ) : stopped ? (
              `Stopped after ${copied.toLocaleString()} message${copied === 1 ? "" : "s"} — the server may copy a few more before it notices.`
            ) : progress?.done ? (
              `✓ Done — ${copied.toLocaleString()} message${copied === 1 ? "" : "s"} copied`
            ) : (
              `Copying… ${copied.toLocaleString()} messages so far`
            )}
            {skipped > 0 && (
              <span title={SKIPPED_TOOLTIP}>
                {` (${skipped.toLocaleString()} skipped — not reproducible byte-for-byte)`}
              </span>
            )}
          </div>
        )}

        {/* Actions */}
        <div className="flex items-center gap-2">
          {running ? (
            <Button
              variant="danger"
              size="sm"
              onClick={handleStop}
              leadingIcon={<Square className="h-3.5 w-3.5" />}
            >
              Stop
            </Button>
          ) : (
            <Button
              variant="primary"
              size="sm"
              onClick={handleStart}
              disabled={!canStart}
              leadingIcon={<Play className="h-3.5 w-3.5" />}
            >
              Start copy
            </Button>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={confirmProd}
        onOpenChange={(v) => setConfirmProd(v)}
        title="Production cluster warning"
        description={`You are copying messages to the production cluster "${effectiveCluster}". This can impact live systems.`}
        confirmLabel="Copy anyway"
        cancelLabel="Cancel"
        variant="danger"
        onConfirm={() => startCopy(true)}
      />
    </>
  );
}

function toLocalDatetimeString(d: Date): string {
  // Returns "YYYY-MM-DDTHH:MM" in local time for use with datetime-local inputs.
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
