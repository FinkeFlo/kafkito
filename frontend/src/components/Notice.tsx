import type { ReactNode } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Info,
  XCircle,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";

export type NoticeIntent = "info" | "success" | "warning" | "danger";

export interface NoticeProps {
  intent: NoticeIntent;
  title?: ReactNode;
  children: ReactNode;
  /** Overrides the default lucide icon for this intent. */
  icon?: ReactNode;
  /** Optional action row (buttons) rendered below the body. */
  actions?: ReactNode;
  className?: string;
}

// Plain tint surfaces (no `/40` opacity). The token table designs these
// for AA-compliant body text on `text-text` in both light and dark
// modes; using the un-opacified surface removes the opacity-composition
// uncertainty that the contrast script can't model. `info` uses the
// accent-subtle tint so it does not visually collide with `warning`
// (the previous mapping shared the amber surface).
const surfaceByIntent: Record<NoticeIntent, string> = {
  info: "bg-accent-subtle border-border",
  success: "bg-tint-green-bg border-border",
  warning: "bg-tint-amber-bg border-border",
  danger: "bg-tint-red-bg border-border",
};

// Foreground tone for the small icon-circle / title accent. Body text
// stays on `text-text` for AA contrast on every tinted surface.
const iconToneByIntent: Record<NoticeIntent, string> = {
  info: "text-accent",
  success: "text-tint-green-fg",
  warning: "text-tint-amber-fg",
  danger: "text-tint-red-fg",
};

// Icon circle uses `bg-panel` so the small emblem reads against the
// surrounding tint surface in both modes (the surface and the icon
// circle would otherwise be the same colour and the badge would vanish).
const iconBgByIntent: Record<NoticeIntent, string> = {
  info: "bg-panel",
  success: "bg-panel",
  warning: "bg-panel",
  danger: "bg-panel",
};

const defaultIcon: Record<NoticeIntent, LucideIcon> = {
  info: Info,
  success: CheckCircle2,
  warning: AlertTriangle,
  danger: XCircle,
};

const ariaRole: Record<NoticeIntent, "status" | "alert"> = {
  info: "status",
  success: "status",
  warning: "alert",
  danger: "alert",
};

/**
 * Tinted callout used for degraded-capability banners (`limited` paths in
 * groups / topics / acls), inline error explanations, and success
 * confirmations that aren't transient toasts. Pairs colour with an icon
 * so the intent survives monochrome rendering.
 */
export function Notice({
  intent,
  title,
  children,
  icon,
  actions,
  className,
}: NoticeProps) {
  const Icon = defaultIcon[intent];
  return (
    <div
      role={ariaRole[intent]}
      className={cn(
        "flex gap-3 rounded-md border p-3 text-sm text-text",
        surfaceByIntent[intent],
        className,
      )}
    >
      <div
        aria-hidden="true"
        className={cn(
          "flex h-7 w-7 shrink-0 items-center justify-center rounded-full",
          iconBgByIntent[intent],
          iconToneByIntent[intent],
        )}
      >
        {icon ?? <Icon className="h-4 w-4" />}
      </div>
      <div className="min-w-0 flex-1 space-y-1">
        {title ? (
          <div className={cn("text-sm font-semibold", iconToneByIntent[intent])}>
            {title}
          </div>
        ) : null}
        <div className="text-sm text-text">{children}</div>
        {actions ? <div className="pt-2">{actions}</div> : null}
      </div>
    </div>
  );
}
