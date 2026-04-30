import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchClusters } from "@/lib/api";

export const Route = createFileRoute("/settings")({
  validateSearch: (s: Record<string, unknown>) => ({
    cluster: typeof s.cluster === "string" ? s.cluster : undefined,
  }),
  component: SettingsLayout,
});

const TABS = [
  { to: "/settings", label: "General", exact: true },
  { to: "/settings/clusters", label: "Clusters", exact: false },
] as const;

function SettingsLayout() {
  const clustersQuery = useQuery({
    queryKey: ["clusters"],
    queryFn: fetchClusters,
    staleTime: 30_000,
  });
  const count = clustersQuery.data?.length ?? 0;

  return (
    <div className="space-y-5 p-6">
      <div>
        <div className="flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted">
          <span className="font-mono normal-case tracking-normal">Workspace</span>
          <span aria-hidden>›</span>
          <span>Settings</span>
        </div>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Settings</h1>
      </div>

      <nav className="flex items-center gap-1 border-b border-border">
        {TABS.map((tab) => (
          <Link
            key={tab.to}
            to={tab.to}
            search={{ cluster: undefined } as never}
            activeOptions={{ exact: tab.exact }}
            className="relative px-3 py-2 text-sm font-medium text-muted transition-colors hover:text-text"
            activeProps={{
              className:
                "relative px-3 py-2 text-sm font-semibold text-text after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-accent",
            }}
          >
            <span>{tab.label}</span>
            {tab.to === "/settings/clusters" && count > 0 && (
              <span className="ml-1 text-[10px] text-muted">{count}</span>
            )}
          </Link>
        ))}
      </nav>

      <Outlet />
    </div>
  );
}
