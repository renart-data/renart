import { createFileRoute } from "@tanstack/react-router";

import { AppPresentationLivePage } from "@/components/app/presentation-page";

export const Route = createFileRoute("/_shell/_presentations/reports/$presentationId/")({
  component: ReportEditorRoute,
});

function ReportEditorRoute() {
  const { presentationId } = Route.useParams();
  return <AppPresentationLivePage kind="report" presentationId={presentationId} />;
}
