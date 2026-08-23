import { createFileRoute } from "@tanstack/react-router";

import { AppSchedulesPage } from "@/components/app/schedules-page";

type SchedulesSearch = {
  pipeline?: string;
};

function normalizeSchedulesSearch(search: Record<string, unknown>): SchedulesSearch {
  return {
    pipeline: typeof search.pipeline === "string" ? search.pipeline : undefined,
  };
}

export const Route = createFileRoute("/_shell/schedules/")({
  validateSearch: normalizeSchedulesSearch,
  component: AppSchedulesRoute,
});

function AppSchedulesRoute() {
  const search = Route.useSearch();
  return <AppSchedulesPage initialQuery={search.pipeline} />;
}
