import { createFileRoute } from "@tanstack/react-router";

import { SemanticDiffDemoPage } from "@/components/app/semantic-diff-demo-page";

export const Route = createFileRoute("/semantic-diff")({
  component: SemanticDiffDemoPage,
});
