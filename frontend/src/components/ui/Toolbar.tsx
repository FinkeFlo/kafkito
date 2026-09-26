import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface ToolbarProps {
  /** Typically a `<SearchInput />` — left-aligned. */
  search?: ReactNode;
  /** Filter dropdowns / checkboxes — sits next to search. */
  filters?: ReactNode;
  /** Buttons or icon buttons — pushed to the right via `ml-auto`. */
  actions?: ReactNode;
  className?: string;
}

/**
 * Filter / action row that sits above data-dense surfaces: search on the
 * left, filters in the middle, actions pushed to the right.
 */
export function Toolbar({ search, filters, actions, className }: ToolbarProps) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>
      {search ? <div className="flex min-w-[260px] flex-1 items-center">{search}</div> : null}
      {filters ? <div className="flex flex-wrap items-center gap-2">{filters}</div> : null}
      {actions ? <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}
