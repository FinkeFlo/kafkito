import {
  useMemo,
  useState,
  type HTMLAttributes,
  type ReactNode,
  type TableHTMLAttributes,
  type ThHTMLAttributes,
} from "react";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronRight } from "lucide-react";
import { clsx } from "clsx";
import { cn } from "@/lib/utils";
import { Skeleton } from "./skeleton";

/**
 * Two table APIs are supported on the same primitive so the kebab and
 * PascalCase consumer waves can converge here:
 *
 *   1. Sub-component composition (`<DataTable title=…><thead>…</thead></DataTable>`).
 *      The route hand-rolls `<thead>` / `<tbody>` / `<tr>` / `<td>` for total
 *      control over alignment and monospace.
 *   2. Column-driven (`<DataTable columns={…} rows={…} rowKey={…} />`).
 *      Sortable, with built-in skeleton + empty-state rendering. `aria-sort`
 *      is announced on sortable columns. Rows are keyboard reachable when
 *      `onRowClick` is set.
 */

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
}

export interface ColumnDrivenProps<Row> {
  columns: DataTableColumn<Row>[];
  rows: Row[] | undefined;
  /** Stable key per row. */
  rowKey: (row: Row) => string;
  /** Click handler — when set, rows render as clickable with chevron. */
  onRowClick?: (row: Row) => void;
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

function DataTableComposition({
  children,
  className,
  title,
  subtitle,
  actions,
}: CompositionProps) {
  return (
    <div
      className={clsx(
        "overflow-hidden rounded-xl border border-border bg-panel",
        className,
      )}
    >
      {(title || actions) && (
        <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3">
          <div className="min-w-0">
            {title && <div className="text-sm font-semibold">{title}</div>}
            {subtitle && (
              <div className="text-xs text-muted">{subtitle}</div>
            )}
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
  isLoading,
  skeletonRows = 5,
  emptyState,
  caption,
  className,
}: ColumnDrivenProps<Row>) {
  const [sortKey, setSortKey] = useState<string | null>(null);
  const [sortDir, setSortDir] = useState<SortDir>("asc");

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
    if (sortKey !== col.id) {
      setSortKey(col.id);
      setSortDir("asc");
    } else if (sortDir === "asc") {
      setSortDir("desc");
    } else {
      setSortKey(null);
    }
  };

  const renderEmpty = !isLoading && rows && rows.length === 0;

  return (
    <div className={cn("space-y-3", className)}>
      {caption ? (
        <div className="text-xs text-muted">{caption}</div>
      ) : null}
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
                          "inline-flex items-center gap-1 transition-colors",
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
              {onRowClick ? <th aria-hidden="true" className="w-10" /> : null}
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
                    return (
                      <tr
                        key={rowKey(row)}
                        onClick={onActivate}
                        onKeyDown={
                          onActivate
                            ? (e) => {
                                if (e.key === "Enter" || e.key === " ") {
                                  e.preventDefault();
                                  onActivate();
                                }
                              }
                            : undefined
                        }
                        tabIndex={onActivate ? 0 : undefined}
                        // `role="button"` (not `link`): we accept Enter
                        // AND Space for activation (button semantics) and
                        // there is no URL to expose for middle/right
                        // click. Phase 3 may wrap rows in `<Link>` once
                        // each route knows the canonical row URL — at
                        // which point the role drops back to default.
                        role={onActivate ? "button" : undefined}
                        className={cn(
                          "group transition-colors duration-150",
                          onActivate && "cursor-pointer hover:bg-hover",
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
                            {col.cell(row)}
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
      className={clsx(
        "bg-subtle text-[11px] uppercase tracking-wider text-muted",
        className,
      )}
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
  ...rest
}: HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr
      className={clsx(
        "border-t border-border transition-colors hover:bg-hover",
        className,
      )}
      {...rest}
    >
      {children}
    </tr>
  );
}
