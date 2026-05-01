import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/schemas/$subject")({
  component: SubjectLayout,
});

function SubjectLayout() {
  return <Outlet />;
}
