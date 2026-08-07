import { createFileRoute } from "@tanstack/react-router";

import { AppSchedulesPage } from "@/components/app/schedules-page";

export const Route = createFileRoute("/_shell/schedules/")({
  component: AppSchedulesPage,
});
