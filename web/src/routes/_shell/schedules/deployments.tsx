import { createFileRoute } from "@tanstack/react-router";

import { AppDeploymentsPage } from "@/components/app/deployments-page";

export const Route = createFileRoute("/_shell/schedules/deployments")({
  component: AppDeploymentsPage,
});
