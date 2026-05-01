import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/brokers")({
  component: BrokersLayout,
});

function BrokersLayout() {
  return <Outlet />;
}
