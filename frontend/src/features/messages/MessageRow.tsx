import { getRouteApi } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { downloadMessageRaw, RawValueMaskedError, type Message } from "@/lib/api";
import type { Token } from "@/lib/path-builder";
import { prettyValue } from "@/lib/format";
import { useFormatters } from "@/lib/use-formatters";
import { Timestamp } from "@/components/ui/Timestamp";
import { StatusIcon } from "@/components/ui/StatusIcon";
import { ReplayModal } from "@/features/messages/ReplayModal";
import { ValueBody } from "@/features/messages/ValueBody";

const routeApi = getRouteApi("/clusters/$cluster/topics/$topic/messages");

export function MessageRow({
  m,
  onPick,
}: {
  m: Message;
  onPick: (trail: Token[], leafValue: unknown) => void;
}) {
  const fmt = useFormatters();
  const { cluster, topic } = routeApi.useParams();
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const [replayOpen, setReplayOpen] = useState(false);
  const preview =
    m.value_encoding === "null"
      ? "(null)"
      : m.value_encoding === "empty"
        ? "(empty)"
        : (m.value ?? "").slice(0, 160);

  const copyValue = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(m.value ?? "");
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore clipboard errors (permissions, insecure context)
    }
  };

  const downloadFull = async (e: React.MouseEvent) => {
    e.stopPropagation();
    setDownloading(true);
    setDownloadError(null);
    try {
      await downloadMessageRaw(cluster, topic, m.partition, m.offset);
    } catch (err) {
      // The list may predate a masking rule that now covers this record.
      if (err instanceof RawValueMaskedError) toast.error(err.message);
      else setDownloadError((err as Error).message);
    } finally {
      setDownloading(false);
    }
  };

  return (
    <div
      data-testid="message-row"
      className="px-4 py-2 text-xs transition-colors hover:bg-[var(--color-surface-hover)]"
    >
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex w-full cursor-pointer flex-wrap items-center gap-2 text-left"
      >
        <span className="font-mono text-[var(--color-text-subtle)]" aria-hidden="true">
          {open ? "▾" : "▸"}
        </span>
        <span className="rounded bg-[var(--color-surface-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-text-muted)]">
          p{m.partition}
        </span>
        <span className="rounded bg-[var(--color-surface-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-text-muted)]">
          #{fmt.number(m.offset)}
        </span>
        <EncodingBadge enc={m.value_encoding} />
        {m.value_sr && <SRBadge meta={m.value_sr} />}
        {m.masked && (
          <span
            title="Value modified by a data_masking rule"
            className="rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-warning)]"
          >
            masked
          </span>
        )}
        {m.value_truncated && (
          <span
            title={`Value truncated to 64 KB preview. Original size: ${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown"}`}
            className="rounded bg-[var(--color-surface-subtle)] px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-text-muted)]"
          >
            preview
          </span>
        )}
        <Timestamp value={m.timestamp_ms} className="text-[10px] text-[var(--color-text-subtle)]" />
        {m.key && (
          <span className="font-mono text-[var(--color-text-muted)]">
            <span className="text-[10px] uppercase tracking-wider text-[var(--color-text-subtle)]">
              key
            </span>{" "}
            {m.key.length > 40 ? m.key.slice(0, 40) + "…" : m.key}
          </span>
        )}
        <span className="flex-1 truncate font-mono text-[var(--color-text)]">{preview}</span>
      </button>
      {open && (
        <div className="mt-3 space-y-3">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <DetailSection
              label={`key · ${m.key === undefined ? "none" : m.key_encoding}`}
              body={m.key === undefined ? "(no key)" : prettyValue(m.key, m.key_encoding)}
              empty={m.key === undefined}
            />
            <DetailSection
              label={`headers${m.headers ? ` · ${fmt.number(Object.keys(m.headers).length)}` : ""}`}
              body={
                m.headers && Object.keys(m.headers).length > 0
                  ? Object.entries(m.headers)
                      .map(([k, v]) => `${k}: ${v}`)
                      .join("\n")
                  : "(no headers)"
              }
              empty={!m.headers || Object.keys(m.headers).length === 0}
            />
          </div>
          <DetailSection
            label={`value · ${m.value_encoding}${m.value_sr ? ` · sr id ${m.value_sr.schema_id ?? "?"}` : ""}${m.value_truncated ? ` · preview only — full size ${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown"}` : ""}`}
            body={<ValueBody m={m} onPick={onPick} cluster={cluster} topic={topic} />}
            action={
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    setReplayOpen(true);
                  }}
                  className="rounded border border-[var(--color-border)] px-2 py-1 text-[11px] hover:border-[var(--color-border-strong)]"
                  title="Replay to another cluster/topic"
                >
                  Replay to…
                </button>
                <button
                  type="button"
                  onClick={copyValue}
                  className="rounded border border-[var(--color-border)] px-2 py-1 text-[11px] hover:border-[var(--color-border-strong)]"
                  title="Copy value to clipboard"
                >
                  {copied ? "Copied!" : "Copy value"}
                </button>
                {m.value_truncated && m.masked && (
                  <>
                    <button
                      type="button"
                      disabled
                      aria-describedby={`download-masked-${m.partition}-${m.offset}`}
                      className="rounded border border-[var(--color-border)] px-2 py-1 text-[11px] disabled:opacity-50"
                    >
                      Download full value
                    </button>
                    <span
                      id={`download-masked-${m.partition}-${m.offset}`}
                      className="text-[11px] text-[var(--color-text-muted)]"
                    >
                      Masked values can&apos;t be downloaded.
                    </span>
                  </>
                )}
                {m.value_truncated && !m.masked && (
                  <button
                    type="button"
                    onClick={downloadFull}
                    disabled={downloading}
                    className="rounded border border-[var(--color-border)] px-2 py-1 text-[11px] hover:border-[var(--color-border-strong)] disabled:opacity-50"
                    title={`Download full value (${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown size"})`}
                  >
                    {downloading ? "Downloading…" : "Download full value"}
                  </button>
                )}
                {downloadError && (
                  <span className="inline-flex items-center gap-1 text-[11px] text-[var(--color-danger)]">
                    <StatusIcon intent="danger" className="h-3 w-3" />
                    {downloadError}
                  </span>
                )}
              </div>
            }
          />
        </div>
      )}
      {replayOpen && (
        <ReplayModal
          open={replayOpen}
          onClose={() => setReplayOpen(false)}
          message={m}
          sourceCluster={cluster}
          sourceTopic={topic}
        />
      )}
    </div>
  );
}

function DetailSection({
  label,
  body,
  empty,
  action,
}: {
  label: string;
  body: React.ReactNode;
  empty?: boolean;
  action?: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-2">
        <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-text-subtle)]">
          {label}
        </div>
        {action}
      </div>
      {typeof body === "string" ? (
        <pre
          className={
            "max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-[var(--color-surface-subtle)] p-3 font-mono text-[11px] leading-relaxed " +
            (empty ? "italic text-[var(--color-text-subtle)]" : "text-[var(--color-text)]")
          }
        >
          {body}
        </pre>
      ) : (
        <div
          className={empty ? "italic text-[var(--color-text-subtle)]" : "text-[var(--color-text)]"}
        >
          {body}
        </div>
      )}
    </div>
  );
}

function EncodingBadge({ enc }: { enc: string }) {
  const styles: Record<string, string> = {
    json: "bg-[var(--color-success-subtle)] text-[var(--color-success)]",
    xml: "bg-[var(--color-success-subtle)] text-[var(--color-success)]",
    text: "bg-[var(--color-surface-subtle)] text-[var(--color-text)]",
    binary: "bg-[var(--color-warning-subtle)] text-[var(--color-warning)]",
    null: "bg-[var(--color-surface-subtle)] text-[var(--color-text-subtle)]",
    empty: "bg-[var(--color-surface-subtle)] text-[var(--color-text-subtle)]",
    avro: "bg-[var(--color-info-subtle)] text-[var(--color-info)]",
    protobuf: "bg-[var(--color-info-subtle)] text-[var(--color-info)]",
    json_schema: "bg-[var(--color-info-subtle)] text-[var(--color-info)]",
  };
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${styles[enc] || "bg-[var(--color-surface-subtle)] text-[var(--color-text)]"}`}
    >
      {enc}
    </span>
  );
}

function SRBadge({
  meta,
}: {
  meta: { format?: string; schema_id?: number; subject?: string; version?: number };
}) {
  const label = meta.subject
    ? `${meta.subject}${meta.version ? `:v${meta.version}` : ""}`
    : meta.schema_id
      ? `id ${meta.schema_id}`
      : "schema";
  const title = `Schema Registry · format=${meta.format ?? "?"} · id=${meta.schema_id ?? "?"}${
    meta.subject ? ` · subject=${meta.subject}` : ""
  }${meta.version ? ` · version=${meta.version}` : ""}`;
  return (
    <span
      title={title}
      className="rounded bg-[var(--color-info-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-info)]"
    >
      sr · {label}
    </span>
  );
}

// Single-section body formatter removed — the row now renders key/value/headers
// as individual DetailSection blocks for better readability and per-field copy.
// The shared value formatter lives in @/lib/format as prettyValue().
