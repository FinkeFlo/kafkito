import { Link } from "@tanstack/react-router";
import { type ReactNode, useMemo } from "react";
import { Card } from "@/components/ui/Card";
import { DataTable, type DataTableColumn } from "@/components/ui/DataTable";
import { MonoId } from "@/components/ui/MonoId";
import { Notice } from "@/components/ui/Notice";
import { Section } from "@/components/ui/Section";
import { Skeleton } from "@/components/ui/Skeleton";
import { Timestamp } from "@/components/ui/Timestamp";
import type { Message } from "@/lib/api";
import {
  type CompareOptions,
  compareMessages,
  type PartComparison,
  type Verdict,
} from "@/lib/message-compare";
import type { FingerprintOptions, FingerprintResult } from "@/lib/message-fingerprint";
import { formatMessageRef, type MessageRef } from "@/lib/message-reference";
import { useFormatters } from "@/lib/use-formatters";
import { type CompareView, messageListLink } from "./compare-search";
import { DiffView } from "./DiffView";
import { HeaderCompareTable } from "./HeaderCompareTable";
import type { ComparedMessage } from "./use-compared-message";
import { useFingerprint } from "./use-fingerprint";
import { VerdictBadge, verdictLabel } from "./VerdictBadge";

type Ready = Extract<ComparedMessage, { status: "ready" }>;

const SUMMARY: Record<Verdict, string> = {
  identical: "Key, value and headers are the same, byte for byte.",
  equivalent:
    "The messages carry the same data; they differ only in formatting or order. The sections below say what was ignored.",
  different: "The messages differ. The sections below show where.",
  not_comparable:
    "Masking or a value only available as a preview keeps this compare from being conclusive.",
};

export function CompareResult({
  refA,
  refB,
  a,
  b,
  options,
  view,
  onlyDiff = false,
}: {
  refA: MessageRef;
  refB: MessageRef;
  a: Ready;
  b: Ready;
  options: CompareOptions;
  view: CompareView;
  /** Hide unchanged lines and rows. */
  onlyDiff?: boolean;
}) {
  const result = useMemo(
    () => compareMessages(a.content, b.content, options),
    [a.content, b.content, options],
  );
  const fpOptions: FingerprintOptions = { ...options, semantic: view === "semantic" };
  const fpA = useFingerprint(a.content, fpOptions);
  const fpB = useFingerprint(b.content, fpOptions);
  const previewNotes = [
    a.previewReason && `Message A: ${a.previewReason}`,
    b.previewReason && `Message B: ${b.previewReason}`,
  ].filter((n): n is string => !!n);

  return (
    <div className="space-y-6">
      <Card data-testid="compare-summary" className="space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-sm font-semibold text-text">Result</h2>
          <VerdictBadge verdict={result.verdict} />
        </div>
        <p className="text-sm text-text">{SUMMARY[result.verdict]}</p>
        <p className="text-xs text-muted">
          Key: {verdictLabel(result.key.verdict)} · Value: {verdictLabel(result.value.verdict)} ·
          Headers: {verdictLabel(result.headers.verdict)}
        </p>
      </Card>

      {previewNotes.length > 0 && (
        <Notice intent="warning" title="Compared on a preview">
          {previewNotes.join(" ")}
        </Notice>
      )}

      <Section title="Metadata" description="Shown for reference; not part of the verdict.">
        <MetadataTable
          refA={refA}
          refB={refB}
          a={a.message}
          b={b.message}
          fpA={fpA}
          fpB={fpB}
          onlyDiff={onlyDiff}
        />
      </Section>

      <PartSection title="Key" what="key" cmp={result.key} view={view} onlyDiff={onlyDiff} />
      <PartSection title="Value" what="value" cmp={result.value} view={view} onlyDiff={onlyDiff} />

      <Section title="Headers" actions={<VerdictBadge verdict={result.headers.verdict} />}>
        <Notes notes={result.headers.notes} />
        <HeaderCompareTable
          rows={
            onlyDiff ? result.headers.rows.filter((r) => r.status !== "same") : result.headers.rows
          }
          filtered={onlyDiff}
        />
      </Section>
    </div>
  );
}

function Notes({ notes }: { notes: string[] }) {
  if (notes.length === 0) return null;
  return (
    <ul className="space-y-1 text-sm text-muted">
      {notes.map((n) => (
        <li key={n}>{n}</li>
      ))}
    </ul>
  );
}

function PartSection({
  title,
  what,
  cmp,
  view,
  onlyDiff,
}: {
  title: string;
  what: string;
  cmp: PartComparison;
  view: CompareView;
  onlyDiff: boolean;
}) {
  const semantic = view === "semantic" && cmp.semantic;
  const texts = semantic ? cmp.semantic : cmp.exact;
  return (
    <Section
      title={title}
      description={
        semantic
          ? "Normalized view: canonical JSON or XML."
          : view === "semantic"
            ? "Shown as stored: no JSON or XML to normalize."
            : "Exact view: as stored."
      }
      actions={<VerdictBadge verdict={cmp.verdict} />}
    >
      <Notes notes={cmp.notes} />
      {texts && (
        <DiffView
          key={`${view}-${onlyDiff}`}
          a={texts.a}
          b={texts.b}
          label={what}
          context={onlyDiff ? 0 : 3}
        />
      )}
    </Section>
  );
}

interface MetaRow {
  label: string;
  a: ReactNode;
  b: ReactNode;
  /** null: not known (yet). */
  differs: boolean | null;
}

const metaColumns: DataTableColumn<MetaRow>[] = [
  { id: "label", header: "Field", cell: (r) => r.label, className: "w-40" },
  // max-w-0 lets a long fingerprint truncate instead of widening the table.
  { id: "a", header: "Message A", cell: (r) => r.a, className: "w-[38%] max-w-0" },
  { id: "b", header: "Message B", cell: (r) => r.b, className: "w-[38%] max-w-0" },
  {
    id: "differs",
    header: "Result",
    cell: (r) =>
      r.differs === null ? (
        <span className="text-subtle-text">—</span>
      ) : r.differs ? (
        <span className="font-semibold text-text">Differs</span>
      ) : (
        <span className="text-muted">Same</span>
      ),
    className: "w-24",
  },
];

function srText(m: Message, which: "key" | "value"): string {
  const sr = which === "key" ? m.key_sr : m.value_sr;
  if (!sr) return "—";
  const subject = sr.subject ? `${sr.subject}${sr.version ? ` v${sr.version}` : ""} · ` : "";
  return `${subject}${sr.format ?? "?"} · id ${sr.schema_id ?? "?"}`;
}

/** The reference, copyable, with a link to the message in its topic. */
function ReferenceCell({ target }: { target: MessageRef }) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <MonoId value={formatMessageRef(target)} tailChars={16} className="min-w-0" />
      <Link {...messageListLink(target)} className="shrink-0 text-xs text-accent hover:underline">
        Open in topic
      </Link>
    </div>
  );
}

function FingerprintCell({ fp }: { fp: FingerprintResult | undefined }) {
  if (!fp) return <Skeleton height="h-4" width="w-40" />;
  if (!fp.ok) return <span className="text-sm text-muted">— {fp.reason}</span>;
  return <MonoId value={fp.fingerprint} tailChars={12} data-testid="fingerprint" />;
}

function MetadataTable({
  refA,
  refB,
  a,
  b,
  fpA,
  fpB,
  onlyDiff,
}: {
  onlyDiff: boolean;
  refA: MessageRef;
  refB: MessageRef;
  a: Message;
  b: Message;
  fpA: FingerprintResult | undefined;
  fpB: FingerprintResult | undefined;
}) {
  const fmt = useFormatters();
  const mono = (s: string) => <span className="font-mono tabular-nums">{s}</span>;
  const size = (m: Message) =>
    m.value_size_bytes === undefined ? "—" : fmt.bytes(m.value_size_bytes);
  const fpDiffers = fpA?.ok && fpB?.ok ? fpA.fingerprint !== fpB.fingerprint : null;
  const rows: MetaRow[] = [
    {
      label: "Reference",
      a: <ReferenceCell target={refA} />,
      b: <ReferenceCell target={refB} />,
      differs: formatMessageRef(refA) !== formatMessageRef(refB),
    },
    {
      label: "Timestamp",
      a: <Timestamp value={a.timestamp_ms} />,
      b: <Timestamp value={b.timestamp_ms} />,
      differs: a.timestamp_ms !== b.timestamp_ms,
    },
    {
      label: "Key encoding",
      a: mono(a.key_encoding),
      b: mono(b.key_encoding),
      differs: a.key_encoding !== b.key_encoding,
    },
    {
      label: "Value encoding",
      a: mono(a.value_encoding),
      b: mono(b.value_encoding),
      differs: a.value_encoding !== b.value_encoding,
    },
    {
      label: "Value schema",
      a: mono(srText(a, "value")),
      b: mono(srText(b, "value")),
      differs: srText(a, "value") !== srText(b, "value"),
    },
    {
      label: "Value size",
      a: mono(size(a)),
      b: mono(size(b)),
      differs: a.value_size_bytes !== b.value_size_bytes,
    },
    {
      label: "Fingerprint",
      a: <FingerprintCell fp={fpA} />,
      b: <FingerprintCell fp={fpB} />,
      differs: fpDiffers,
    },
  ];
  return (
    <DataTable
      columns={metaColumns}
      rows={onlyDiff ? rows.filter((r) => r.differs !== false) : rows}
      rowKey={(r) => r.label}
      emptyState={<p className="py-8 text-center text-sm text-muted">No metadata differs.</p>}
    />
  );
}
