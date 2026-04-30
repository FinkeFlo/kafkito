import { createRootRouteWithContext } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { CommandPalette } from "@/components/CommandPalette";
import { Shell } from "@/components/Shell";
import { Toaster } from "@/components/toaster";
import { TooltipProvider } from "@/components/tooltip";

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
