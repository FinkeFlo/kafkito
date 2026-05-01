import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/groups/$group")({
  component: GroupLayout,
});

function GroupLayout() {
  return <Outlet />;
}
