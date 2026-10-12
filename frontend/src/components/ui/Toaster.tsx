import { Toaster as SonnerToaster } from "sonner";
// Loaded as a stylesheet instead of sonner's runtime <style> injection, which
// the CSP blocks (see sonnerExternalStyles in vite.config.ts).
import "sonner/dist/styles.css";
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
      // Clear of the compare bar while it is shown (it sets the variable).
      offset={{ right: 24, bottom: "calc(24px + var(--compare-bar-height, 0px))" }}
      mobileOffset={{ bottom: "calc(16px + var(--compare-bar-height, 0px))" }}
      toastOptions={{
        classNames: {
          toast: "rounded-xl border border-border bg-panel text-text shadow-lg",
          description: "text-muted",
        },
      }}
    />
  );
}
