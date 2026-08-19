import { createFileRoute } from "@tanstack/react-router";

import { AppPresentationLivePage } from "@/components/app/presentation-page";

export const Route = createFileRoute("/_shell/_presentations/dashboards/$presentationId/")({
  component: DashboardEditorRoute,
});

function DashboardEditorRoute() {
  const { presentationId } = Route.useParams();
  return <AppPresentationLivePage kind="dashboard" presentationId={presentationId} />;
}
