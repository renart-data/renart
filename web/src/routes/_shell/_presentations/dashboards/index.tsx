import { createFileRoute } from "@tanstack/react-router";

import { AppPresentationsIndexPage } from "@/components/app/presentation-page";

export const Route = createFileRoute("/_shell/_presentations/dashboards/")({
  component: () => <AppPresentationsIndexPage kind="dashboard" />,
});
