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
      offset={24}
      toastOptions={{
        classNames: {
          toast: "rounded-xl border border-border bg-panel text-text shadow-lg",
          description: "text-muted",
        },
      }}
    />
  );
}
