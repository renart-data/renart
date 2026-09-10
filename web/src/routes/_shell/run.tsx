import { createFileRoute, useNavigate } from "@tanstack/react-router";

import {
  AppRunOverviewPage,
  normalizeAppRunOverviewSearch,
} from "@/components/app/run-overview-page";

export const Route = createFileRoute("/_shell/run")({
  validateSearch: normalizeAppRunOverviewSearch,
  component: AppRunOverviewRoute,
});

function AppRunOverviewRoute() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  return (
    <AppRunOverviewPage
      search={search}
      onSearchChange={(next) => void navigate({ search: next, replace: true })}
    />
  );
}
