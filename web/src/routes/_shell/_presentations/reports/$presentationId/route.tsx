import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_shell/_presentations/reports/$presentationId")({
  component: Outlet,
});
