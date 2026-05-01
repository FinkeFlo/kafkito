import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters")({
  component: ClustersLayout,
});

function ClustersLayout() {
  return <Outlet />;
}
