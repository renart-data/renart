import { createFileRoute, useLocation, useNavigate, useParams } from "@tanstack/react-router";

import { AppBuildPage } from "@/components/app/build-page";
import {
  normalizeAppBuildSearch,
  appAssetViewPath,
  appBuildViewFromPath,
} from "@/components/app/build-route-model";

export const Route = createFileRoute("/_shell/pipelines/$pipelineId")({
  validateSearch: normalizeAppBuildSearch,
  component: AppPipelineLayoutRoute,
});

function AppPipelineLayoutRoute() {
  const { pipelineId } = Route.useParams();
  const allParams = useParams({ strict: false }) as { assetId?: string };
  const search = Route.useSearch();
  const navigate = useNavigate();
  const location = useLocation();
  const currentView = appBuildViewFromPath(location.pathname);
  const updateSearch = (nextSearch: Partial<typeof search>) => {
    navigate({
      to: location.pathname as never,
      search: { ...search, ...nextSearch } as never,
      replace: true,
    });
  };

  return (
    <AppBuildPage
      pipelineId={pipelineId}
      selectedAssetId={allParams.assetId}
      resultTab={search.result ?? "inspect"}
      editorMode={search.editor ?? "asset"}
      onResultTabChange={(result) => updateSearch({ result })}
      onAssetSelect={(assetId) =>
        navigate({
          to: appAssetViewPath(allParams.assetId ? currentView : "split"),
          params: { pipelineId, assetId },
          search: {
            ...search,
            result: search.result === "query" ? "inspect" : search.result,
            editor: "asset",
          },
        })
      }
    />
  );
}
