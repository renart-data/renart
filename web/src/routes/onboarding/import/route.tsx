import { Outlet, createFileRoute } from "@tanstack/react-router";

import { loadOnboardingRouteContext } from "@/src/routes/-onboarding-shared";

export const Route = createFileRoute("/onboarding/import")({
  beforeLoad: loadOnboardingRouteContext,
  component: OnboardingImportLayoutRouteComponent,
});

function OnboardingImportLayoutRouteComponent() {
  return <Outlet />;
}
