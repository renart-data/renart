import { createFileRoute } from "@tanstack/react-router";

import {
  AppPresentationViewerPage,
  normalizePresentationViewerSearch,
} from "@/components/app/presentation-viewer";

export const Route = createFileRoute("/_shell/_presentations/dashboards/$presentationId/view")({
  validateSearch: normalizePresentationViewerSearch,
  component: DashboardViewerRoute,
});

function DashboardViewerRoute() {
  const { presentationId } = Route.useParams();
  const { filters } = Route.useSearch();
  return (
    <AppPresentationViewerPage
      kind="dashboard"
      presentationId={presentationId}
      filterSearch={filters}
    />
  );
}
