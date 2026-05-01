import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/schemas")({
  component: SchemasLayout,
});

function SchemasLayout() {
  return <Outlet />;
}
