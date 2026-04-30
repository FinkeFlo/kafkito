import { Toaster as SonnerToaster, toast } from "sonner";
import { useTheme } from "@/lib/use-theme";

/**
 * Project-wide toast container. Mount once in the root layout.
 * Theme follows the app's resolved theme (uses the same `data-theme` token).
 */
export function Toaster() {
  const { theme } = useTheme();
  return (
    <SonnerToaster
      position="bottom-right"
      theme={theme}
      richColors
      closeButton
      duration={4000}
      visibleToasts={3}
      offset={24}
      toastOptions={{
        classNames: {
          toast:
            "rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-raised)] text-[var(--color-text)] shadow-lg",
          description: "text-[var(--color-text-muted)]",
        },
      }}
    />
  );
}

export { toast };
