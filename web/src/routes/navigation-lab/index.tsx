import { Navigate, createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/navigation-lab/")({
  component: NavigationLabIndex,
});

function NavigationLabIndex() {
  return <Navigate to="/navigation-lab/$variant" params={{ variant: "workbench" }} />;
}
