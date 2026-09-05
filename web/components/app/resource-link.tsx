import { Link, useLocation } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useAtomValue } from "jotai";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { selectedEnvironmentAtom } from "@/lib/atoms/workspace";
import type { ResourceTarget } from "@/lib/generated/api-types";
import { getPinnedProjectId } from "@/lib/project-context";
import { detailSearch, parseDetail, resourceLabel } from "@/lib/resource-navigation";

// Real navigation, not a resolution command. The generated href carries all
// context required by a fresh tab; ordinary clicks keep the primary route.
export function ResourceLink({
  target,
  environment,
  children,
  className,
}: {
  target?: ResourceTarget;
  environment?: string;
  children?: ReactNode;
  className?: string;
}) {
  const location = useLocation();
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const { workspaceConfig } = useWorkspaceSettingsData();
  const project = getPinnedProjectId() ?? workspaceConfig?.project_id;
  if (!target || !project) return null;
  let detail;
  try {
    detail = parseDetail({
      v: 1,
      environment: environment || selectedEnvironment || workspaceConfig?.default_environment,
      target,
    });
  } catch {
    return null;
  }
  return (
    <Link
      to="."
      search={detailSearch(location.search, project, detail)}
      replace={
        JSON.stringify((location.search as { detail?: unknown }).detail) === JSON.stringify(detail)
      }
      preload={false}
      data-resource-link="true"
      className={
        className ??
        "ml-1.5 inline-flex text-primary underline decoration-primary/40 underline-offset-2 hover:decoration-primary"
      }
      aria-label={
        children
          ? undefined
          : detail.target.kind === "asset-column"
            ? `Edit type of ${detail.target.column}`
            : resourceLabel(detail.target)
      }
    >
      {children ?? resourceLabel(detail.target)}
    </Link>
  );
}
