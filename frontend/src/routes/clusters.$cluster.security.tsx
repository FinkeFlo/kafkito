import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { PageHeader } from "@/components/page-header";

export const Route = createFileRoute("/clusters/$cluster/security")({
  component: SecurityLayout,
});

function SecurityLayout() {
  const { cluster } = Route.useParams();
  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={
          <>
            <span className="font-mono normal-case tracking-normal">{cluster}</span>{" "}
            <span aria-hidden>›</span> Security
          </>
        }
        title="Security"
        subtitle="Principals, ACLs, and SCRAM credentials for this cluster."
      />
      <nav className="flex items-center gap-1 border-b border-border">
        <Link
          to="/clusters/$cluster/security/acls"
          params={{ cluster }}
          className="relative px-3 py-2 text-sm font-medium text-muted transition-colors hover:text-text"
          activeProps={{
            className:
              "relative px-3 py-2 text-sm font-semibold text-text after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-accent",
          }}
        >
          ACLs
        </Link>
        <Link
          to="/clusters/$cluster/security/users"
          params={{ cluster }}
          className="relative px-3 py-2 text-sm font-medium text-muted transition-colors hover:text-text"
          activeProps={{
            className:
              "relative px-3 py-2 text-sm font-semibold text-text after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-accent",
          }}
        >
          SCRAM users
        </Link>
      </nav>
      <Outlet />
    </div>
  );
}
