import { createFileRoute } from "@tanstack/react-router";

import { OnboardingRoutePage } from "@/components/onboarding-route-page";
import { loadOnboardingRouteContext } from "@/src/routes/-onboarding-shared";

export const Route = createFileRoute("/onboarding/quickstart")({
  beforeLoad: loadOnboardingRouteContext,
  component: OnboardingQuickstartRouteComponent,
});

function OnboardingQuickstartRouteComponent() {
  const { onboardingState } = Route.useRouteContext();
  return <OnboardingRoutePage onboardingState={onboardingState} />;
}
