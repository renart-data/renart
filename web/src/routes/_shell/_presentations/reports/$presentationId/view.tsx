import { createFileRoute } from "@tanstack/react-router";

import {
  AppPresentationViewerPage,
  normalizePresentationViewerSearch,
} from "@/components/app/presentation-viewer";

export const Route = createFileRoute("/_shell/_presentations/reports/$presentationId/view")({
  validateSearch: normalizePresentationViewerSearch,
  component: ReportViewerRoute,
});

function ReportViewerRoute() {
  const { presentationId } = Route.useParams();
  const { filters } = Route.useSearch();
  return (
    <AppPresentationViewerPage
      kind="report"
      presentationId={presentationId}
      filterSearch={filters}
    />
  );
}
