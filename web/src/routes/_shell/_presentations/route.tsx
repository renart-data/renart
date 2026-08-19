import { createFileRoute } from "@tanstack/react-router";

import { AppPresentationsLayout } from "@/components/app/presentation-page";

export const Route = createFileRoute("/_shell/_presentations")({
  component: AppPresentationsLayout,
});
