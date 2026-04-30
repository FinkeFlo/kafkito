import { useEffect, useState, type ReactNode } from "react";
import { Button } from "./button";
import { Modal } from "./Modal";

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  /** When set, the user must type this exact value to enable the confirm button. */
  confirmPhrase?: string;
  confirmLabel?: ReactNode;
  cancelLabel?: ReactNode;
  /**
   * Color of the confirm button. Default `"danger"`. The legacy alias
   * `"destructive"` is still accepted so route callsites compile while
   * Phase 3 migrates them.
   */
  variant?: "danger" | "destructive" | "primary";
  /** Async-aware: prevents double clicks while resolving. */
  onConfirm: () => void | Promise<void>;
}

/**
 * Confirm dialog. Built on the canonical `<Modal>` (focus trap, Escape,
 * focus restore handled there). Supports an optional "type the phrase"
 * gating step for high-blast-radius operations.
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmPhrase,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  variant = "danger",
  onConfirm,
}: ConfirmDialogProps) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) {
      setTyped("");
      setBusy(false);
    }
  }, [open]);

  if (!open) return null;

  const phraseOk = !confirmPhrase || typed === confirmPhrase;
  // Map the legacy `destructive` alias onto the canonical `danger` button
  // variant so the underlying Button only sees the canonical name.
  const buttonVariant = variant === "destructive" ? "danger" : variant;

  const handleConfirm = async () => {
    if (busy || !phraseOk) return;
    setBusy(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={() => {
        if (busy) return;
        onOpenChange(false);
      }}
      title={title}
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            {cancelLabel}
          </Button>
          <Button
            variant={buttonVariant}
            onClick={handleConfirm}
            loading={busy}
            disabled={!phraseOk}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      {description ? (
        <p className="text-sm text-muted">{description}</p>
      ) : null}
      {confirmPhrase ? (
        <label className="mt-4 block space-y-1.5 text-xs font-medium text-muted">
          <span>
            Type <span className="font-mono text-text">{confirmPhrase}</span> to confirm
          </span>
          <input
            name="confirm-phrase"
            autoComplete="off"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            className="h-9 w-full rounded-md border border-border bg-panel px-3 font-mono text-sm text-text"
          />
        </label>
      ) : null}
    </Modal>
  );
}
