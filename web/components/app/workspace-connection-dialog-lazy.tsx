import { lazy, Suspense, type ComponentProps } from "react";

const ConnectionDialog = lazy(() =>
  import("./workspace-connection-dialog").then((module) => ({
    default: module.WorkspaceConnectionDialog,
  })),
);

// Connection authoring is a command, not a dependency of every canvas visit.
export function WorkspaceConnectionDialog(props: ComponentProps<typeof ConnectionDialog>) {
  if (!props.open) return null;
  return (
    <Suspense fallback={null}>
      <ConnectionDialog {...props} />
    </Suspense>
  );
}
