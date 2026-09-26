import { createRootRouteWithContext } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { CommandPalette } from "@/features/shell/CommandPalette";
import { Shell } from "@/features/shell/Shell";
import { Toaster } from "@/components/ui/toaster";
import { TooltipProvider } from "@/components/ui/tooltip";

interface RouterContext {
  queryClient: QueryClient;
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
});

function RootLayout() {
  return (
    <TooltipProvider>
      <Shell />
      <CommandPalette />
      <Toaster />
    </TooltipProvider>
  );
}
