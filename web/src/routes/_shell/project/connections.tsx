import { createFileRoute } from "@tanstack/react-router";

import { AppProjectConnectionsPage } from "@/components/app/settings-pages";
import { normalizeSettingsSearch } from "@/lib/settings-navigation";

export const Route = createFileRoute("/_shell/project/connections")({
  validateSearch: (search) => normalizeSettingsSearch(search, "connections"),
  component: AppProjectConnectionsRoute,
});

function AppProjectConnectionsRoute() {
  const search = Route.useSearch();
  return (
    <AppProjectConnectionsPage
      selectedEnvironment={search.environment}
      selectedConnection={search.connection}
      action={search.action}
    />
  );
}
