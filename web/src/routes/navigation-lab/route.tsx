import { Outlet, createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/navigation-lab")({
  component: NavigationLabLayout,
});

function NavigationLabLayout() {
  return <Outlet />;
}
