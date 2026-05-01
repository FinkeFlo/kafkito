import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/groups")({
  component: GroupsLayout,
});

function GroupsLayout() {
  return <Outlet />;
}
