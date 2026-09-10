import { createFileRoute } from "@tanstack/react-router";

import { AppProjectEnvironmentsPage } from "@/components/app/settings-pages";
import { normalizeSettingsSearch } from "@/lib/settings-navigation";

export const Route = createFileRoute("/_shell/project/environments")({
  validateSearch: (search) => normalizeSettingsSearch(search, "environments"),
  component: () => <AppProjectEnvironmentsPage {...Route.useSearch()} />,
});
