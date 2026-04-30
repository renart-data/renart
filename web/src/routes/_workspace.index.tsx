import { Outlet, createFileRoute, redirect } from "@tanstack/react-router";

import { WorkspacePage } from "@/components/workspace-page";
import { getOnboardingState } from "@/lib/api";

export const Route = createFileRoute("/_workspace/")({
  beforeLoad: async () => {
    const state = await getOnboardingState();
    if (!state.active) {
      return;
    }

    switch (state.step) {
      case "start":
        throw redirect({ to: "/onboarding" });
      case "connection-type":
        throw redirect({ to: "/onboarding" });
      case "connection-config":
        throw redirect({ to: "/onboarding/import/connection" });
      case "import":
        throw redirect({ to: "/onboarding/import/review" });
      case "quickstart":
        throw redirect({ to: "/onboarding" });
      case "success":
        throw redirect({ to: "/onboarding/success" });
      default:
        throw redirect({ to: "/onboarding" });
    }
  },
  validateSearch: (search: Record<string, unknown>) => ({
    pipeline:
      typeof search.pipeline === "string" ? search.pipeline : undefined,
    asset: typeof search.asset === "string" ? search.asset : undefined,
    environment:
      typeof search.environment === "string" ? search.environment : undefined,
  }),
  component: WorkspaceIndexRouteComponent,
});

function WorkspaceIndexRouteComponent() {
  return (
    <>
      <WorkspacePage />
      <Outlet />
    </>
  );
}
