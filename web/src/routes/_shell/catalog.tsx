import { createFileRoute } from "@tanstack/react-router";

import { AppCatalogPage, normalizeAppCatalogSearch } from "@/components/app/catalog-page";

export const Route = createFileRoute("/_shell/catalog")({
  validateSearch: normalizeAppCatalogSearch,
  component: AppCatalogRoute,
});

function AppCatalogRoute() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <AppCatalogPage
      selectedAssetId={search.asset}
      onAssetSelect={(asset) => navigate({ search: { ...search, asset }, replace: true })}
    />
  );
}
