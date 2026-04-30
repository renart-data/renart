import { createFileRoute } from "@tanstack/react-router";

import { OnboardingRoutePage } from "@/components/onboarding-route-page";

export const Route = createFileRoute("/onboarding/")({
  component: OnboardingIndexRouteComponent,
});

function OnboardingIndexRouteComponent() {
  const { onboardingState } = Route.useRouteContext();
  return <OnboardingRoutePage onboardingState={{ ...onboardingState, step: "start" }} />;
}
