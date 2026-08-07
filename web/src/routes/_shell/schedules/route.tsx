import { createFileRoute } from "@tanstack/react-router";

import { AppSchedulesLayout } from "@/components/app/schedules-layout";

export const Route = createFileRoute("/_shell/schedules")({
  component: AppSchedulesLayout,
});
