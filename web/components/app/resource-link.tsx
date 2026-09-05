import { Link, useLocation } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useResourceNavigation } from "@/hooks/use-resource-navigation";
import type { ResourceTarget } from "@/lib/generated/api-types";
import { resourceLabel } from "@/lib/resource-navigation";

// Real navigation, not a resolution command. The generated href carries all
// context required by a fresh tab; ordinary clicks reveal the existing owner UI.
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
  const { destination } = useResourceNavigation();
  let next;
  try {
    next = target ? destination(target, environment) : undefined;
  } catch {
    return null;
  }
  if (!next) return null;
  const detail = next.search.detail as import("@/lib/resource-navigation").ResourceDetail;
  return (
    <Link
      to={next.pathname}
      search={next.search}
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
            ? `${resourceLabel(detail.target)} of ${detail.target.column}`
            : resourceLabel(detail.target)
      }
    >
      {children ?? resourceLabel(detail.target)}
    </Link>
  );
}
