import { DataTable, type DataTableColumn } from "@/components/ui/DataTable";
import type { HeaderContent, HeaderRow, HeaderStatus } from "@/lib/message-compare";
import { MaskedBadge } from "@/features/messages/MaskedBadge";

const STATUS_TEXT: Record<HeaderStatus, string> = {
  same: "Same",
  different: "Different",
  only_a: "Only in A",
  only_b: "Only in B",
};

function HeaderValue({ h }: { h?: HeaderContent }) {
  if (!h) return <span className="text-subtle-text">—</span>;
  return (
    <span className="break-all font-mono">
      {h.text}
      {h.masked && (
        <>
          {" "}
          <MaskedBadge title="Header value modified by a data_masking rule" />
        </>
      )}
    </span>
  );
}

const columns: DataTableColumn<HeaderRow>[] = [
  {
    id: "key",
    header: "Header",
    cell: (r) => <span className="break-all font-mono text-[13px]">{r.key}</span>,
    className: "w-1/5",
  },
  { id: "a", header: "Message A", cell: (r) => <HeaderValue h={r.a} /> },
  { id: "b", header: "Message B", cell: (r) => <HeaderValue h={r.b} /> },
  {
    id: "status",
    header: "Result",
    cell: (r) => (
      <span className={r.status === "same" ? "text-muted" : "font-semibold text-text"}>
        {STATUS_TEXT[r.status]}
      </span>
    ),
    className: "w-32",
  },
];

/** Header-by-header comparison; the result column carries the outcome in words. */
export function HeaderCompareTable({
  rows,
  filtered = false,
}: {
  rows: HeaderRow[];
  /** `rows` holds only the differing headers. */
  filtered?: boolean;
}) {
  return (
    <DataTable
      columns={columns}
      rows={rows}
      rowKey={(r) => r.key}
      emptyState={
        <p className="py-8 text-center text-sm text-muted">
          {filtered ? "No header differs." : "Neither message has headers."}
        </p>
      }
    />
  );
}
