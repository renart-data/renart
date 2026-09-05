import { useEffect } from "react";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { workspaceAtom } from "@/lib/atoms/workspace";
import type { ResourceSearch } from "@/lib/resource-navigation";
import { resourceDestination } from "@/lib/ui-navigation";

// Compatibility for copied v1 links whose pathname still names their origin.
// All UI is rendered by the real owner page, never a second detail outlet.
export function ResourceNavigation() {
  const location = useLocation();
  const navigate = useNavigate();
  const workspace = useAtomValue(workspaceAtom);
  const { project, detail } = location.search as ResourceSearch;
  let next: ReturnType<typeof resourceDestination> | undefined;
  let error: string | undefined;
  if (project && detail && workspace) {
    try {
      next = resourceDestination(location, project, detail, workspace);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : "This location is unavailable.";
    }
  }
  const pathname = next?.pathname;
  const search = JSON.stringify(next?.search);
  useEffect(() => {
    if (
      pathname &&
      search &&
      (pathname !== location.pathname || search !== JSON.stringify(location.search))
    )
      void navigate({ to: pathname, search: JSON.parse(search), replace: true });
  }, [pathname, search, location.pathname, location.search, navigate]);
  return error ? (
    <p role="alert" className="px-4 py-2 text-sm text-destructive">
      {error}
    </p>
  ) : null;
}
