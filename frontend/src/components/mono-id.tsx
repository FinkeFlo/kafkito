import { Check, Copy } from "lucide-react";
import { useState, type HTMLAttributes } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { Tooltip } from "./tooltip";

export interface MonoIdProps extends Omit<HTMLAttributes<HTMLSpanElement>, "children"> {
  value: string;
  /**
   * Number of trailing characters that are always kept visible.
   * Useful for Kafka identifiers that share a long common prefix
   * but differ in their suffix (e.g. `consumer-<long>-<hash>`).
   */
  tailChars?: number;
  /** Show a copy-to-clipboard button on hover. Defaults to true. */
  copyable?: boolean;
  /** Render as muted text. */
  muted?: boolean;
  /** Optional placeholder when value is empty. */
  placeholder?: string;
}

/**
 * Single-line monospace identifier with middle-ellipsis and a real tooltip
 * showing the full value. Clicking the trailing copy button copies the value.
 */
export function MonoId({
  value,
  tailChars = 8,
  copyable = true,
  muted = false,
  placeholder = "—",
  className,
  ...rest
}: MonoIdProps) {
  const [copied, setCopied] = useState(false);
  const { t } = useTranslation("common");

  if (!value) {
    return (
      <span className={cn("text-[var(--color-text-subtle)]", className)} {...rest}>
        {placeholder}
      </span>
    );
  }

  const split = value.length > tailChars + 4;
  const head = split ? value.slice(0, value.length - tailChars) : value;
  const tail = split ? value.slice(-tailChars) : "";

  const onCopy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      // clipboard may be blocked; swallow silently
    }
  };

  return (
    <Tooltip
      content={
        <span className="break-all font-mono text-[11px] leading-snug">{value}</span>
      }
    >
      <span
        className={cn(
          "group inline-flex min-w-0 max-w-full items-center align-middle font-mono",
          muted ? "text-[var(--color-text-muted)]" : undefined,
          className,
        )}
        {...rest}
      >
        <span className="min-w-0 truncate">{head}</span>
        {tail ? <span className="shrink-0">{tail}</span> : null}
        {copyable ? (
          <button
            type="button"
            onClick={onCopy}
            aria-label={
              copied
                ? t("copy.done", { defaultValue: "Copied" })
                : `${t("copy.idle", { defaultValue: "Copy" })} ${value}`
            }
            className={cn(
              "ml-1 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded text-[var(--color-text-subtle)]",
              "opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100",
              "hover:text-[var(--color-text)]",
            )}
          >
            {copied ? <Check className="h-3 w-3 text-[var(--color-success)]" /> : <Copy className="h-3 w-3" />}
          </button>
        ) : null}
      </span>
    </Tooltip>
  );
}
