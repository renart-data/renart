import { createFileRoute } from "@tanstack/react-router";

import { AppDataBrowserPage } from "@/components/app/data-browser/data-browser";

export const Route = createFileRoute("/_shell/data")({
  component: AppDataBrowserPage,
});
