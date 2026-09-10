import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_shell/schedules/timeline")({
  beforeLoad: () => {
    throw redirect({ to: "/run", replace: true });
  },
});
