import { createFileRoute } from "@tanstack/react-router";

import { AppPresentationsLayout } from "@/components/app/presentation-page";

export const Route = createFileRoute("/_shell/_presentations")({
  validateSearch: (
    search: Record<string, unknown>,
  ): { presentation_editor?: "visual" | "definition" } => ({
    presentation_editor:
      search.presentation_editor === "definition"
        ? ("definition" as const)
        : search.presentation_editor === "visual"
          ? ("visual" as const)
          : undefined,
  }),
  component: AppPresentationsLayout,
});
