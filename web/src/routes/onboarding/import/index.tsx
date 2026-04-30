import { createFileRoute } from "@tanstack/react-router";

import { OnboardingRoutePage } from "@/components/onboarding-route-page";

export const Route = createFileRoute("/onboarding/import/")({
  component: OnboardingImportTypeRouteComponent,
});

function OnboardingImportTypeRouteComponent() {
  const { onboardingState } = Route.useRouteContext();
  return <OnboardingRoutePage onboardingState={onboardingState} />;
}
