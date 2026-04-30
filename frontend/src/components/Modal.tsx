import { useCallback, useEffect, useId, useRef, type ReactNode } from "react";
import { cn } from "@/lib/utils";

export type ModalSize = "sm" | "md" | "lg";

export interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  /** Optional action row rendered in the footer, right-aligned. */
  actions?: ReactNode;
  size?: ModalSize;
  /** Pass an element id to populate `aria-describedby` on the dialog. */
  ariaDescribedBy?: string;
  /** Override the panel className (e.g. extra padding). */
  className?: string;
}

const sizeMap: Record<ModalSize, string> = {
  sm: "max-w-sm",
  md: "max-w-md",
  lg: "max-w-2xl",
};

const FOCUSABLE_SELECTOR = [
  "button:not([disabled])",
  "[href]",
  "input:not([disabled]):not([type='hidden'])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

function isVisible(el: HTMLElement): boolean {
  if (el.hidden) return false;
  if (el.getAttribute("aria-hidden") === "true") return false;
  // `offsetParent` is null for `display:none`. Modal itself is `position:
  // fixed` so we treat the children's offsetParent presence as visible
  // proxy; for inputs inside a `position:fixed` panel offsetParent is the
  // panel element.
  return el.offsetParent !== null || el === document.activeElement;
}

function focusableInside(panel: HTMLElement): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
    isVisible,
  );
}

/**
 * Module-level stack of currently-open modals. Both Escape-to-close and
 * the focus-trap Tab handler must apply only to the topmost modal so a
 * nested Modal (e.g. <ConfirmDialog> inside another Modal's `actions`)
 * doesn't double-close on a single Escape press, and so Tab cycles
 * inside the inner panel instead of leaking to the outer.
 */
type ModalStackEntry = { id: number };
const modalStack: ModalStackEntry[] = [];

/**
 * Centered modal panel with backdrop, focus-trap, Escape-to-close, and
 * focus-restore. Implemented inline (no `react-focus-lock` /
 * `focus-trap-react` dependency). Body scroll is locked while open.
 */
export function Modal({
  open,
  onClose,
  title,
  children,
  actions,
  size = "md",
  ariaDescribedBy,
  className,
}: ModalProps) {
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  // Hold the latest `onClose` in a ref so the trap effect can call it
  // without re-depending on the prop. Consumers always pass an inline
  // arrow (`onClose={() => setOpen(false)}`) which is a fresh function
  // each render — listing `onClose` in the effect's deps would re-run
  // the install/cleanup loop on every render and silently break focus
  // restore (cleanup restores to the previous render's
  // `document.activeElement`, which after the first re-run is an
  // element inside the modal itself).
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  });

  // Body scroll lock + focus trap installation. Runs once per `open`
  // transition — install on `open=true`, cleanup on `open=false` /
  // unmount.
  useEffect(() => {
    if (!open) return;

    previousFocusRef.current = document.activeElement as HTMLElement | null;

    // Register this modal on the module-level stack. Both Escape and
    // the focus-trap Tab handler short-circuit when this entry is not
    // the topmost — that's how nested modals (e.g. ConfirmDialog inside
    // ResetOffsetsModal) avoid double-closing or leaking Tab focus to
    // the outer panel.
    const stackEntry: ModalStackEntry = { id: Date.now() + Math.random() };
    modalStack.push(stackEntry);

    // Move focus into the panel on next paint so the panel is mounted.
    const focusFrame = window.requestAnimationFrame(() => {
      const panel = panelRef.current;
      if (!panel) return;
      const focusable = focusableInside(panel);
      if (focusable.length > 0) {
        focusable[0].focus();
      } else {
        panel.focus();
      }
    });

    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    const onKey = (e: KeyboardEvent) => {
      // Only the topmost modal handles keyboard. Nested Modals each
      // attach a `document` listener; without this gate one Escape
      // press would close every modal on the stack.
      if (modalStack[modalStack.length - 1] !== stackEntry) return;
      if (e.key === "Escape") {
        e.preventDefault();
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab") return;
      const panel = panelRef.current;
      if (!panel) return;
      const focusable = focusableInside(panel);
      if (focusable.length === 0) {
        // Trap focus on the panel itself.
        e.preventDefault();
        panel.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement as HTMLElement | null;
      if (e.shiftKey) {
        if (active === first || !panel.contains(active)) {
          e.preventDefault();
          last.focus();
        }
      } else {
        if (active === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };

    document.addEventListener("keydown", onKey);
    return () => {
      window.cancelAnimationFrame(focusFrame);
      document.removeEventListener("keydown", onKey);
      const idx = modalStack.indexOf(stackEntry);
      if (idx !== -1) modalStack.splice(idx, 1);
      // Restore body scroll only when the last modal closes; nested
      // modals share the lock.
      if (modalStack.length === 0) {
        document.body.style.overflow = previousOverflow;
      }
      const previous = previousFocusRef.current;
      if (previous && document.contains(previous)) {
        previous.focus();
      } else {
        document.body.focus?.();
      }
    };
  }, [open]);

  const onBackdropClick = useCallback(() => {
    onCloseRef.current();
  }, []);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-40">
      <div
        aria-hidden="true"
        className="fixed inset-0 z-40 bg-overlay"
        onClick={onBackdropClick}
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={ariaDescribedBy}
        tabIndex={-1}
        className={cn(
          "fixed left-1/2 top-1/2 z-50 w-full -translate-x-1/2 -translate-y-1/2",
          "rounded-xl border border-border bg-panel shadow-xl",
          sizeMap[size],
          className,
        )}
      >
        <div className="border-b border-border px-5 py-4">
          <h2
            id={titleId}
            className="text-base font-semibold tracking-tight text-text"
          >
            {title}
          </h2>
        </div>
        <div className="px-5 py-4 text-sm text-text">{children}</div>
        {actions ? (
          <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
            {actions}
          </div>
        ) : null}
      </div>
    </div>
  );
}
