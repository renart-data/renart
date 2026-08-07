import { createFileRoute } from "@tanstack/react-router";

import { AppRunTimelinePage } from "@/components/app/run-timeline-page";

export const Route = createFileRoute("/_shell/schedules/timeline")({
  component: AppRunTimelinePage,
});
