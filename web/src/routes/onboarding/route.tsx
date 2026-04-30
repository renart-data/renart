import { Outlet, createFileRoute } from "@tanstack/react-router";

import { loadOnboardingRouteContext } from "@/src/routes/-onboarding-shared";

export const Route = createFileRoute("/onboarding")({
  beforeLoad: loadOnboardingRouteContext,
  component: OnboardingLayoutRouteComponent,
});

function OnboardingLayoutRouteComponent() {
  return <Outlet />;
}
