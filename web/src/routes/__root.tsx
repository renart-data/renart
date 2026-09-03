import { Outlet, createRootRoute, useLocation } from "@tanstack/react-router";
import { useEffect } from "react";

import { AppProviders } from "@/src/providers";

export const Route = createRootRoute({
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
