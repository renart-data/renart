import { createFileRoute } from "@tanstack/react-router";

import { NavigationLabPage } from "@/components/app/navigation-lab/navigation-lab-page";
import type { LabVariant } from "@/components/app/navigation-lab/navigation-lab-data";

const variants = new Set<LabVariant>(["workbench", "lifecycle", "studio"]);

export const Route = createFileRoute("/navigation-lab/$variant")({
  component: NavigationLabVariantRoute,
});

function NavigationLabVariantRoute() {
  const { variant } = Route.useParams();
  const resolvedVariant: LabVariant = variants.has(variant as LabVariant)
    ? (variant as LabVariant)
    : "workbench";

  return <NavigationLabPage variant={resolvedVariant} />;
}
