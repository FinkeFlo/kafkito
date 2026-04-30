import { isValidElement, type ComponentType, type ReactNode } from "react";
import type { LucideProps } from "lucide-react";
import { clsx } from "clsx";

/**
 * Canonical empty-state surface. Accepts either a Lucide component
 * (`icon={Boxes}`) or any ReactNode (`icon={<Users />}`) so legacy and
 * Direction-A callsites can converge on this primitive.
 */
export type EmptyStateIcon = ComponentType<LucideProps> | ReactNode;

export interface EmptyStateProps {
  icon?: EmptyStateIcon;
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  className?: string;
}

function renderIcon(icon: EmptyStateIcon | undefined): ReactNode {
  if (!icon) return null;
  if (isValidElement(icon)) return icon;
  // Lucide icons are `forwardRef` objects — `typeof` returns `"object"`,
  // not `"function"`. Both shapes are valid React component types.
  const isComponentType =
    typeof icon === "function" ||
    (typeof icon === "object" && icon !== null && "$$typeof" in icon);
  if (isComponentType) {
    const Icon = icon as ComponentType<LucideProps>;
    return <Icon className="h-7 w-7 text-accent" />;
  }
  return icon as ReactNode;
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: EmptyStateProps) {
  const iconNode = renderIcon(icon);
  return (
    <div
      className={clsx(
        "flex flex-col items-center justify-center rounded-xl border border-border bg-panel px-6 py-16 text-center",
        className,
      )}
    >
      {iconNode ? (
        <div className="flex h-14 w-14 items-center justify-center rounded-xl bg-accent-subtle text-accent">
          {iconNode}
        </div>
      ) : null}
      <h2 className="mt-5 text-lg font-semibold tracking-tight">{title}</h2>
      {description && (
        <p className="mt-2 max-w-md text-sm text-muted">{description}</p>
      )}
      {action && <div className="mt-6 flex justify-center gap-2">{action}</div>}
    </div>
  );
}
