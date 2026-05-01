import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/topics")({
  component: TopicsLayout,
});

function TopicsLayout() {
  return <Outlet />;
}
