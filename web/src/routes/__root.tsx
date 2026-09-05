import { Outlet, createRootRoute, retainSearchParams, useLocation } from "@tanstack/react-router";
import { useEffect } from "react";

import { AppProviders } from "@/src/providers";
import { normalizeResourceSearch } from "@/lib/resource-navigation";
import { bootstrapProjectRoute } from "@/lib/project-route-bootstrap";

export const Route = createRootRoute({
  validateSearch: normalizeResourceSearch,
  search: { middlewares: [retainSearchParams(["project", "detail"])] },
  beforeLoad: ({ search }) => bootstrapProjectRoute(search.project),
  errorComponent: ({ error }) => (
    <main className="mx-auto max-w-lg p-8">
      <h1 className="text-lg font-medium">This link could not be opened</h1>
      <p role="alert" className="mt-3 text-sm">
        {error.message}
      </p>
      <a href="/" className="mt-4 inline-block text-sm text-primary underline">
        Open Renart home
      </a>
    </main>
  ),
  component: RootComponent,
});

function RootComponent() {
  const location = useLocation();

  useEffect(() => {
    const title = getDocumentTitle(location.pathname);
    if (!title) {
      return;
    }

    document.title = title;
  }, [location.pathname]);

  return (
    <AppProviders>
      <Outlet />
    </AppProviders>
  );
}

function getDocumentTitle(pathname: string) {
  if (pathname.startsWith("/navigation-lab")) {
    return "Navigation study · renart";
  }
  if (pathname.startsWith("/semantic-diff")) {
    return "Semantic Diff · renart";
  }

  if (pathname.startsWith("/dashboards")) {
    return "Dashboards · renart";
  }

  if (pathname.startsWith("/reports")) {
    return "Reports · renart";
  }

  if (pathname.includes("/connections")) {
    return "Connections · Settings · renart";
  }

  if (pathname.startsWith("/settings")) {
    return "Environments · Settings · renart";
  }

  return null;
}
