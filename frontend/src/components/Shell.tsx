import { Link, Outlet } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Boxes,
  FileJson,
  Home,
  Moon,
  Search,
  Server,
  Shield,
  Sun,
  Users,
} from "lucide-react";
import { clsx } from "clsx";
import { ClusterPill } from "./ClusterPill";
import { openCommandPalette } from "./CommandPalette";
import { UserMenu } from "./UserMenu";
import { useTheme } from "@/lib/theme";
import { fetchInfo } from "@/lib/api";
import { useCluster } from "@/lib/use-cluster";

function Logo() {
  return (
    <svg width="32" height="32" viewBox="0 0 48 48" aria-hidden>
      <rect width="48" height="48" rx="10" fill="var(--color-text)" />
      <rect x="11" y="10" width="5" height="28" rx="1.5" fill="var(--color-panel)" />
      <rect x="19" y="15" width="18" height="3" rx="1.5" fill="var(--color-panel)" />
      <rect x="19" y="22.5" width="14" height="3" rx="1.5" fill="var(--color-panel)" />
      <rect x="19" y="30" width="18" height="3" rx="1.5" fill="var(--color-accent)" />
    </svg>
  );
}

const navLinkBase =
  "relative px-3 py-2 text-[13px] font-medium text-muted transition-colors hover:text-text " +
  "[&.active]:font-semibold [&.active]:text-text " +
  "after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-accent after:opacity-0 [&.active]:after:opacity-100";

function SearchButton() {
  return (
    <button
      type="button"
      onClick={openCommandPalette}
      className="group hidden h-8 items-center gap-2 rounded-md border border-border bg-panel px-2.5 text-xs transition-colors hover:bg-hover md:inline-flex"
      title="Open command palette"
    >
      <Search className="h-3.5 w-3.5 text-subtle-text" />
      <span className="text-muted">
        Find <span className="font-mono text-text/80">topic, group, broker</span>…
      </span>
      <kbd className="ml-1 rounded border border-border bg-subtle px-1 font-mono text-[10px] text-muted">
        ⌘K
      </kbd>
    </button>
  );
}

function ThemeButton() {
  const { theme, toggle } = useTheme();
  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
      className="flex h-8 w-8 items-center justify-center rounded-md border border-border bg-panel text-muted transition-colors hover:bg-hover"
      title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
    >
      {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
    </button>
  );
}


function VersionBadge() {
  const infoQuery = useQuery({
    queryKey: ["info"],
    queryFn: fetchInfo,
    staleTime: 5 * 60_000,
  });
  const version = infoQuery.data?.version ?? "—";
  return (
    <div className="leading-tight">
      <div className="text-[15px] font-semibold tracking-tight">kafkito</div>
      <div className="text-[11px] text-subtle-text">console · v{version}</div>
    </div>
  );
}

export function Shell() {
  const { cluster } = useCluster();
  return (
    <div className="flex min-h-full flex-col bg-bg text-text">
      <header className="border-b border-border bg-panel">
        <div className="flex items-center justify-between gap-4 px-6 pt-3">
          <Link
            to="/clusters"
            search={{ cluster: undefined }}
            aria-label="Fleet overview"
            className="flex items-center gap-2.5 rounded-md transition-opacity hover:opacity-90"
          >
            <Logo />
            <VersionBadge />
          </Link>
          <div className="flex items-center gap-2">
            <ClusterPill />
            <SearchButton />
            <div className="hidden h-5 w-px bg-border md:block" />
            <ThemeButton />
            <UserMenu />
          </div>
        </div>
        <nav className="flex items-center gap-1 px-6 pb-0 pt-3">
          <Link
            to="/"
            activeOptions={{ exact: true }}
            className={navLinkBase}
          >
            <Home className="mr-1.5 inline h-3.5 w-3.5 -translate-y-px" />
            Overview
          </Link>
          {cluster ? (
            <>
              <Link
                to="/clusters/$cluster/topics"
                params={{ cluster }}
                className={navLinkBase}
              >
                <Boxes className="mr-1.5 inline h-3.5 w-3.5 -translate-y-px" />
                Topics
              </Link>
              <Link
                to="/clusters/$cluster/groups"
                params={{ cluster }}
                search={{ group: undefined }}
                className={navLinkBase}
              >
                <Users className="mr-1.5 inline h-3.5 w-3.5 -translate-y-px" />
                Consumer groups
              </Link>
              <Link
                to="/clusters/$cluster/schemas"
                params={{ cluster }}
                search={{ subject: undefined, version: undefined }}
                className={navLinkBase}
              >
                <FileJson className="mr-1.5 inline h-3.5 w-3.5 -translate-y-px" />
                Schemas
              </Link>
              <Link
                to="/clusters/$cluster/security/acls"
                params={{ cluster }}
                className={clsx(navLinkBase, "mr-auto")}
              >
                <Shield className="mr-1.5 inline h-3.5 w-3.5 -translate-y-px" />
                ACLs
              </Link>
            </>
          ) : (
            <Link
              to="/clusters"
              search={{ cluster: undefined }}
              className={clsx(navLinkBase, "mr-auto")}
            >
              <Boxes className="mr-1.5 inline h-3.5 w-3.5 -translate-y-px" />
              Clusters
            </Link>
          )}
          <div className="flex items-center gap-1 pb-1">
            {cluster ? (
              <Link
                to="/clusters/$cluster/brokers"
                params={{ cluster }}
                className="inline-flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium text-muted transition-colors hover:bg-hover hover:text-text [&.active]:bg-accent-subtle [&.active]:text-accent"
              >
                <Server className="h-3.5 w-3.5" />
                Brokers
              </Link>
            ) : null}
          </div>
        </nav>
      </header>
      <main className="flex-1">
        <Outlet />
      </main>
    </div>
  );
}
