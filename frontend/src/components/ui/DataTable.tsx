import {
  useMemo,
  useState,
  type HTMLAttributes,
  type MouseEvent,
  type ReactNode,
  type TableHTMLAttributes,
  type ThHTMLAttributes,
} from "react";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronRight } from "lucide-react";
import { clsx } from "clsx";
import { cn } from "@/lib/utils";
import { Skeleton } from "./Skeleton";

/**
 * Two table APIs are supported on the same primitive so the kebab and
 * PascalCase consumer waves can converge here:
 *
 *   1. Sub-component composition (`<DataTable title=…><thead>…</thead></DataTable>`).
 *      The route hand-rolls `<thead>` / `<tbody>` / `<tr>` / `<td>` for total
 *      control over alignment and monospace.
 *   2. Column-driven (`<DataTable columns={…} rows={…} rowKey={…} />`).
 *      Sortable, with built-in skeleton + empty-state rendering. `aria-sort`
 *      is announced on sortable columns.
 *
 * Clickable rows keep their table semantics: the `<tr>` stays a row and is
 * never a tab stop. The row's action lives in a real control in the primary
 * cell (a `<Link>` or `<button>` marked `data-row-primary`), which is what
 * keyboard and screen-reader users reach. A pointer click elsewhere on the
 * row is forwarded to that control as a mouse convenience. Column mode
 * renders the `<button>` itself when `onRowClick` is set; composition mode
 * passes `clickable` to `<DataTableRow>` and marks its own control.
 */

// Controls inside a clickable row that handle their own clicks.
const OWN_CLICK_SELECTOR =
  "a, button, input, select, textarea, label, summary, [role='button'], [role='link'], [contenteditable='true']";

/** Forwards a pointer click on a row to the row's `data-row-primary` control. */
function forwardRowClick(e: MouseEvent<HTMLTableRowElement>) {
  const own = (e.target as Element).closest(OWN_CLICK_SELECTOR);
  if (own && e.currentTarget.contains(own)) return;
  e.currentTarget.querySelector<HTMLElement>("[data-row-primary]")?.click();
}

// ---------------------------------------------------------------------------
// Column-driven mode
// ---------------------------------------------------------------------------

export interface DataTableColumn<Row> {
  id: string;
  header: ReactNode;
  /** Cell renderer. */
  cell: (row: Row) => ReactNode;
  /** Optional accessor that returns a sortable scalar value. */
  sortValue?: (row: Row) => string | number | bigint | null | undefined;
  /** Tailwind classes for the <td>/<th> (e.g. "w-32", "text-right"). */
  className?: string;
  align?: "left" | "right";
  /** Hosts the row's `<button>` when `onRowClick` is set. Defaults to the first column. */
  primary?: boolean;
}

export interface DataTableSort {
  key: string;
  dir: "asc" | "desc";
}

export interface ColumnDrivenProps<Row> {
  columns: DataTableColumn<Row>[];
  rows: Row[] | undefined;
  /** Stable key per row. */
  rowKey: (row: Row) => string;
  /**
   * Row action — when set, the primary column's cell renders as a real
   * `<button>` (Tab to it, Enter / Space activates), the rest of the row
   * forwards pointer clicks to it, and a chevron column is added.
   */
  onRowClick?: (row: Row) => void;
  /** Exposes the row action as a disclosure (`aria-expanded`), e.g. an inline detail panel. */
  isRowExpanded?: (row: Row) => boolean;
  /**
   * For rows whose action is a control the cell renders itself (e.g. a
   * `<Link>` marked `data-row-primary`): adds the row hover and forwards
   * pointer clicks to it, without the built-in button and chevron.
   */
  clickableRows?: boolean;
  /**
   * Controlled sort, e.g. to keep it in the URL. Pass both; the table then
   * reports clicks through `onSortChange` instead of keeping its own state.
   */
  sort?: DataTableSort | null;
  onSortChange?: (next: DataTableSort | null) => void;
  /** Async loading flag. Renders skeleton rows. */
  isLoading?: boolean;
  /** Number of skeleton rows to render while loading. */
  skeletonRows?: number;
  /** Empty state node, rendered inside the table when rows is [] and not loading. */
  emptyState?: ReactNode;
  /** Optional caption rendered above the table (e.g. "Showing x of y"). */
  caption?: ReactNode;
  className?: string;
  // Composition-mode props are absent in this shape.
  children?: undefined;
  title?: undefined;
  subtitle?: undefined;
  actions?: undefined;
}

// ---------------------------------------------------------------------------
// Composition mode
// ---------------------------------------------------------------------------

export interface CompositionProps {
  children: ReactNode;
  className?: string;
  title?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  // Column-mode props are absent in this shape.
  columns?: undefined;
  rows?: undefined;
  rowKey?: undefined;
  onRowClick?: undefined;
  isRowExpanded?: undefined;
  clickableRows?: undefined;
  sort?: undefined;
  onSortChange?: undefined;
  isLoading?: undefined;
  skeletonRows?: undefined;
  emptyState?: undefined;
  caption?: undefined;
}

export type DataTableProps<Row> = ColumnDrivenProps<Row> | CompositionProps;

type SortDir = "asc" | "desc";

export function DataTable<Row>(props: DataTableProps<Row>) {
  if ((props as ColumnDrivenProps<Row>).columns) {
    return <DataTableColumnView {...(props as ColumnDrivenProps<Row>)} />;
  }
  return <DataTableComposition {...(props as CompositionProps)} />;
}

// ---------------------------------------------------------------------------
// Composition-mode renderer (sub-component shell)
// ---------------------------------------------------------------------------

function DataTableComposition({ children, className, title, subtitle, actions }: CompositionProps) {
  return (
    <div className={clsx("overflow-hidden rounded-xl border border-border bg-panel", className)}>
      {(title || actions) && (
        <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3">
          <div className="min-w-0">
            {title && <div className="text-sm font-semibold">{title}</div>}
            {subtitle && <div className="text-xs text-muted">{subtitle}</div>}
          </div>
          {actions && <div className="flex items-center gap-2">{actions}</div>}
        </div>
      )}
      <div className="overflow-x-auto">
        <table className="w-full text-sm">{children}</table>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Column-driven renderer
// ---------------------------------------------------------------------------

function DataTableColumnView<Row>({
  columns,
  rows,
  rowKey,
  onRowClick,
  isRowExpanded,
  clickableRows,
  sort,
  onSortChange,
  isLoading,
  skeletonRows = 5,
  emptyState,
  caption,
  className,
}: ColumnDrivenProps<Row>) {
  const [ownSort, setOwnSort] = useState<DataTableSort | null>(null);
  const controlled = onSortChange !== undefined;
  const current = controlled ? (sort ?? null) : ownSort;
  const sortKey = current?.key ?? null;
  const sortDir: SortDir = current?.dir ?? "asc";

  const sorted = useMemo(() => {
    if (!rows || !sortKey) return rows ?? [];
    const col = columns.find((c) => c.id === sortKey);
    if (!col?.sortValue) return rows;
    const dir = sortDir === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const va = col.sortValue!(a);
      const vb = col.sortValue!(b);
      if (va === vb) return 0;
      if (va === null || va === undefined) return 1;
      if (vb === null || vb === undefined) return -1;
      return va < vb ? -dir : dir;
    });
  }, [rows, columns, sortKey, sortDir]);

  const toggleSort = (col: DataTableColumn<Row>) => {
    if (!col.sortValue) return;
    const next: DataTableSort | null =
      sortKey !== col.id
        ? { key: col.id, dir: "asc" }
        : sortDir === "asc"
          ? { key: col.id, dir: "desc" }
          : null;
    if (controlled) onSortChange(next);
    else setOwnSort(next);
  };

  const renderEmpty = !isLoading && rows && rows.length === 0;
  const primaryId = onRowClick ? (columns.find((c) => c.primary) ?? columns[0])?.id : undefined;

  return (
    <div className={cn("space-y-3", className)}>
      {caption ? <div className="text-xs text-muted">{caption}</div> : null}
      <div className="overflow-x-auto rounded-xl border border-border bg-panel">
        <table className="w-full text-sm">
          <thead className="bg-subtle text-[11px] uppercase tracking-wider text-muted">
            <tr>
              {columns.map((col) => {
                const active = sortKey === col.id;
                const sortable = !!col.sortValue;
                const Icon = !active ? ArrowUpDown : sortDir === "asc" ? ArrowUp : ArrowDown;
                const ariaSort: "ascending" | "descending" | "none" | undefined = sortable
                  ? active
                    ? sortDir === "asc"
                      ? "ascending"
                      : "descending"
                    : "none"
                  : undefined;
                return (
                  <th
                    key={col.id}
                    scope="col"
                    aria-sort={ariaSort}
                    className={cn(
                      "px-4 py-2 text-left font-semibold",
                      col.align === "right" && "text-right",
                      col.className,
                    )}
                  >
                    {sortable ? (
                      <button
                        type="button"
                        onClick={() => toggleSort(col)}
                        className={cn(
                          // Browsers reset text-transform on <button> and the
                          // preflight does not inherit it, so restate the
                          // header's uppercase.
                          "inline-flex items-center gap-1 uppercase transition-colors",
                          active ? "text-text" : "hover:text-text",
                        )}
                      >
                        {col.header}
                        <Icon className="h-3 w-3" aria-hidden />
                      </button>
                    ) : (
                      col.header
                    )}
                  </th>
                );
              })}
              {onRowClick ? (
                <th className="w-10">
                  <span className="sr-only">Open</span>
                </th>
              ) : null}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {isLoading
              ? Array.from({ length: skeletonRows }).map((_, i) => (
                  <tr key={`sk-${i}`}>
                    {columns.map((c) => (
                      <td key={c.id} className="px-4 py-2.5">
                        <Skeleton height="h-4" />
                      </td>
                    ))}
                    {onRowClick ? <td className="w-10" /> : null}
                  </tr>
                ))
              : renderEmpty
                ? null
                : sorted.map((row) => {
                    const onActivate = onRowClick ? () => onRowClick(row) : undefined;
                    const forwards = !!onActivate || !!clickableRows;
                    return (
                      <tr
                        key={rowKey(row)}
                        onClick={forwards ? forwardRowClick : undefined}
                        className={cn(
                          "group transition-colors duration-150",
                          forwards && "cursor-pointer hover:bg-hover",
                        )}
                      >
                        {columns.map((col) => (
                          <td
                            key={col.id}
                            className={cn(
                              "px-4 py-2.5 align-middle text-text",
                              col.align === "right" && "text-right",
                              col.className,
                            )}
                          >
                            {onActivate && col.id === primaryId ? (
                              <button
                                type="button"
                                data-row-primary=""
                                onClick={onActivate}
                                aria-expanded={isRowExpanded?.(row)}
                                className="cursor-pointer text-left"
                              >
                                {col.cell(row)}
                              </button>
                            ) : (
                              col.cell(row)
                            )}
                          </td>
                        ))}
                        {onActivate ? (
                          <td className="w-10 pr-3 text-right">
                            <ChevronRight
                              className="ml-auto h-4 w-4 text-subtle-text group-hover:text-muted"
                              aria-hidden
                            />
                          </td>
                        ) : null}
                      </tr>
                    );
                  })}
          </tbody>
        </table>
        {renderEmpty && emptyState ? <div>{emptyState}</div> : null}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sub-component primitives (composition mode helpers)
// ---------------------------------------------------------------------------

export function DataTableHead({
  children,
  className,
  ...rest
}: TableHTMLAttributes<HTMLTableSectionElement>) {
  return (
    <thead
      className={clsx("bg-subtle text-[11px] uppercase tracking-wider text-muted", className)}
      {...rest}
    >
      {children}
    </thead>
  );
}

export function DataTableTh({
  children,
  className,
  align = "left",
  ...rest
}: ThHTMLAttributes<HTMLTableCellElement> & { align?: "left" | "right" | "center" }) {
  return (
    <th
      scope="col"
      className={clsx(
        "px-4 py-2 font-semibold",
        align === "left" && "text-left",
        align === "right" && "text-right",
        align === "center" && "text-center",
        className,
      )}
      {...rest}
    >
      {children}
    </th>
  );
}

export function DataTableRow({
  children,
  className,
  clickable,
  ...rest
}: HTMLAttributes<HTMLTableRowElement> & {
  /**
   * Forwards pointer clicks on the row to its `data-row-primary` control
   * (a `<Link>` or `<button>` in the primary cell). The row itself stays a
   * plain row: no role override, no tab stop.
   */
  clickable?: boolean;
}) {
  return (
    <tr
      className={clsx(
        "border-t border-border transition-colors hover:bg-hover",
        clickable && "cursor-pointer",
        className,
      )}
      onClick={clickable ? forwardRowClick : undefined}
      {...rest}
    >
      {children}
    </tr>
  );
}
