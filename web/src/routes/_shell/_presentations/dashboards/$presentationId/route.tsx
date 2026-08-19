import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_shell/_presentations/dashboards/$presentationId")({
  component: Outlet,
});
