import { getRouteApi } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { downloadMessageRaw, RawValueMaskedError, type Message } from "@/lib/api";
import type { Token } from "@/lib/path-builder";
import { prettyValue } from "@/lib/format";
import { useFormatters } from "@/lib/use-formatters";
import { Timestamp } from "@/components/ui/Timestamp";
import { StatusIcon } from "@/components/ui/StatusIcon";
import { Tag } from "@/components/ui/Tag";
import { sameMessage, useCompareSelection } from "@/lib/compare-selection";
import { ReplayModal } from "@/features/messages/ReplayModal";
import { CompareAction } from "@/features/messages/compare/CompareAction";
import { AuditSnapshotAction } from "@/features/messages/AuditSnapshotAction";
import { ValueBody } from "@/features/messages/ValueBody";
import { MaskedBadge } from "@/features/messages/MaskedBadge";
import type { FieldFilter } from "@/features/messages/use-click-to-filter";

const routeApi = getRouteApi("/clusters/$cluster/topics/$topic/messages");

export function MessageRow({
  m,
  onPick,
  onFilter,
}: {
  m: Message;
  onPick: (trail: Token[], leafValue: unknown) => void;
  onFilter: (filter: FieldFilter) => void;
}) {
  const fmt = useFormatters();
  const { cluster, topic } = routeApi.useParams();
  const target = { cluster, topic, partition: m.partition, offset: m.offset };
  const selectedForCompare = sameMessage(useCompareSelection(), target);
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const [replayOpen, setReplayOpen] = useState(false);
  const maskedHeaders = new Set(m.masked_headers ?? []);
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
    <div data-testid="message-row" className="px-4 py-2 text-xs transition-colors hover:bg-hover">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex w-full cursor-pointer flex-wrap items-center gap-2 text-left"
      >
        <span className="font-mono text-subtle-text" aria-hidden="true">
          {open ? "▾" : "▸"}
        </span>
        <span className="rounded bg-subtle px-1.5 py-0.5 font-mono text-[10px] text-muted">
          p{m.partition}
        </span>
        <span className="rounded bg-subtle px-1.5 py-0.5 font-mono text-[10px] text-muted">
          #{fmt.number(m.offset)}
        </span>
        <EncodingBadge enc={m.value_encoding} />
        {m.value_sr && <SRBadge meta={m.value_sr} />}
        {m.masked && <MaskedBadge title="Value modified by a data_masking rule" />}
        {m.key_masked && (
          <MaskedBadge label="key masked" title="Key modified by a data_masking rule" />
        )}
        {maskedHeaders.size > 0 && (
          <MaskedBadge
            label="headers masked"
            title={`Header values modified by a data_masking rule: ${[...maskedHeaders].join(", ")}`}
          />
        )}
        {m.value_truncated && (
          <span
            title={`Value truncated to 64 KB preview. Original size: ${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown"}`}
            className="rounded bg-subtle px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-muted"
          >
            preview
          </span>
        )}
        {selectedForCompare && <Tag variant="info">selected for compare</Tag>}
        <Timestamp value={m.timestamp_ms} className="text-[10px] text-subtle-text" />
        {m.key && (
          <span className="font-mono text-muted">
            <span className="text-[10px] uppercase tracking-wider text-subtle-text">key</span>{" "}
            {m.key.length > 40 ? m.key.slice(0, 40) + "…" : m.key}
          </span>
        )}
        <span className="flex-1 truncate font-mono text-text">{preview}</span>
      </button>
      {open && (
        <div className="mt-3 space-y-3">
          {/* Actions on the whole message: key, value and headers. */}
          <div className="flex flex-wrap items-center justify-end gap-1.5">
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                setReplayOpen(true);
              }}
              className="rounded border border-border px-2 py-1 text-[11px] hover:border-border-hover"
              title="Replay to another cluster/topic"
            >
              Replay to…
            </button>
            <CompareAction target={target} />
            <AuditSnapshotAction m={m} cluster={cluster} topic={topic} />
          </div>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <DetailSection
              label={`key · ${m.key === undefined ? "none" : m.key_encoding}`}
              body={m.key === undefined ? "(no key)" : prettyValue(m.key, m.key_encoding)}
              empty={m.key === undefined}
              action={
                m.key_masked ? (
                  <MaskedBadge title="Key modified by a data_masking rule" label="masked" />
                ) : (
                  m.key && (
                    <FilterButton onClick={() => onFilter({ kind: "key", value: m.key ?? "" })}>
                      Search by key
                    </FilterButton>
                  )
                )
              }
            />
            <HeadersSection
              headers={m.headers}
              masked={maskedHeaders}
              id={`headers-${m.partition}-${m.offset}`}
            />
          </div>
          <DetailSection
            label={`value · ${m.value_encoding}${m.value_sr ? ` · sr id ${m.value_sr.schema_id ?? "?"}` : ""}${m.value_truncated ? ` · preview only — full size ${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown"}` : ""}`}
            body={<ValueBody m={m} onPick={onPick} cluster={cluster} topic={topic} />}
            action={
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={copyValue}
                  className="rounded border border-border px-2 py-1 text-[11px] hover:border-border-hover"
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
                      className="rounded border border-border px-2 py-1 text-[11px] disabled:opacity-50"
                    >
                      Download full value
                    </button>
                    <span
                      id={`download-masked-${m.partition}-${m.offset}`}
                      className="text-[11px] text-muted"
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
                    className="rounded border border-border px-2 py-1 text-[11px] hover:border-border-hover disabled:opacity-50"
                    title={`Download full value (${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown size"})`}
                  >
                    {downloading ? "Downloading…" : "Download full value"}
                  </button>
                )}
                {downloadError && (
                  <span className="inline-flex items-center gap-1 text-[11px] text-danger">
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

const SECTION_LABEL = "text-[10px] font-semibold uppercase tracking-wider text-subtle-text";

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
        <div className={SECTION_LABEL}>{label}</div>
        {action}
      </div>
      {typeof body === "string" ? (
        <pre
          className={
            "max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-subtle p-3 font-mono text-[11px] leading-relaxed " +
            (empty ? "italic text-subtle-text" : "text-text")
          }
        >
          {body}
        </pre>
      ) : (
        <div className={empty ? "italic text-subtle-text" : "text-text"}>{body}</div>
      )}
    </div>
  );
}

/**
 * The headers of a message, collapsed to a one-line toggle until the user
 * opens them: they often outnumber and outsize the key.
 */
function HeadersSection({
  headers,
  masked,
  id,
}: {
  headers: Record<string, string> | undefined;
  masked: Set<string>;
  id: string;
}) {
  const fmt = useFormatters();
  const [open, setOpen] = useState(false);
  const entries = Object.entries(headers ?? {});
  if (entries.length === 0) return <DetailSection label="headers" body="(no headers)" empty />;
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        aria-controls={id}
        className={`mb-1 inline-flex cursor-pointer items-center gap-1 ${SECTION_LABEL} hover:text-text`}
      >
        <span className="font-mono" aria-hidden="true">
          {open ? "▾" : "▸"}
        </span>
        headers · <span className="tabular-nums">{fmt.number(entries.length)}</span>
      </button>
      {open && (
        <ul
          id={id}
          className="max-h-96 space-y-0.5 overflow-auto rounded-md bg-subtle p-3 font-mono text-[11px] leading-relaxed text-text"
        >
          {entries.map(([k, v]) => (
            <li key={k} data-testid="header-line" className="break-all whitespace-pre-wrap">
              {`${k}: ${v}`}
              {masked.has(k) && (
                <>
                  {" "}
                  <MaskedBadge title="Header value modified by a data_masking rule" />
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** A small action that turns the key into a search. */
function FilterButton({ children, onClick }: { children: React.ReactNode; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="rounded border border-border px-2 py-1 font-sans text-[11px] text-muted hover:border-border-hover hover:text-text"
    >
      {children}
    </button>
  );
}

function EncodingBadge({ enc }: { enc: string }) {
  const styles: Record<string, string> = {
    json: "bg-tint-green-bg text-success",
    xml: "bg-tint-green-bg text-success",
    text: "bg-subtle text-text",
    binary: "bg-tint-amber-bg text-warning",
    null: "bg-subtle text-subtle-text",
    empty: "bg-subtle text-subtle-text",
    avro: "bg-accent-subtle text-accent",
    protobuf: "bg-accent-subtle text-accent",
    json_schema: "bg-accent-subtle text-accent",
  };
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${styles[enc] || "bg-subtle text-text"}`}
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
      className="rounded bg-accent-subtle px-1.5 py-0.5 font-mono text-[10px] text-accent"
    >
      sr · {label}
    </span>
  );
}

// Single-section body formatter removed — the row now renders key/value/headers
// as individual DetailSection blocks for better readability and per-field copy.
// The shared value formatter lives in @/lib/format as prettyValue().
