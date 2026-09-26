import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface SectionProps {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
  children?: ReactNode;
}

export function Section({ title, description, actions, className, children }: SectionProps) {
  return (
    <section className={cn("space-y-4", className)}>
      {(title || actions || description) && (
        <header className="flex items-end justify-between gap-4">
          <div className="space-y-1">
            {title ? (
              <h2 className="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
                {title}
              </h2>
            ) : null}
            {description ? (
              <p className="text-sm text-[var(--color-text-muted)]">{description}</p>
            ) : null}
          </div>
          {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
        </header>
      )}
      {children}
    </section>
  );
}
