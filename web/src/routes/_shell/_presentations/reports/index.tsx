import { createFileRoute } from "@tanstack/react-router";

import { AppPresentationsIndexPage } from "@/components/app/presentation-page";

export const Route = createFileRoute("/_shell/_presentations/reports/")({
  component: () => <AppPresentationsIndexPage kind="report" />,
});
