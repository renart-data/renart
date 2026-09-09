import { createFileRoute } from "@tanstack/react-router";

import { AppNotebookLivePage } from "@/components/app/notebook-page";

export const Route = createFileRoute("/_shell/notebooks/$notebookId")({
  validateSearch: (search: Record<string, unknown>): { notebook_nav?: "library" } => ({
    notebook_nav: search.notebook_nav === "library" ? "library" : undefined,
  }),
  component: AppNotebookRoute,
});

function AppNotebookRoute() {
  const { notebookId } = Route.useParams();
  const { notebook_nav } = Route.useSearch();
  return <AppNotebookLivePage notebookId={notebookId} libraryOpen={notebook_nav === "library"} />;
}
