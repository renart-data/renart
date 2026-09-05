import { resourceHref, detailSearch, type ResourceDetail } from "./resource-navigation";

export type NavigationOwners = {
  pipelines: { id: string; assets: { id: string }[] }[];
  presentations?: { workspace_id: string; kind: string }[];
};

// Each target owns only its page and the state needed to reveal it. In
// particular, property navigation does not own the result tab or editor mode.
export function resourceDestination(
  current: { pathname: string; search: Record<string, unknown> },
  project: string,
  detail: ResourceDetail,
  owners: NavigationOwners,
) {
  const target = detail.target;
  const search: Record<string, unknown> = detailSearch(current.search, project, detail);
  let pathname: string;
  switch (target.kind) {
    case "asset-column":
    case "asset-section": {
      const matches = owners.pipelines.filter((p) =>
        p.assets.some((a) => a.id === target.asset_id),
      );
      if (matches.length !== 1)
        throw new Error("The linked asset is no longer uniquely available.");
      const existingView = /\/pipelines\/[^/]+\/(?:assets\/[^/]+\/)?(canvas|split|code)$/.exec(
        current.pathname,
      )?.[1];
      let view = existingView ?? "split";
      if (target.kind === "asset-section" && target.section === "source") {
        search.editor = "asset";
        if (view === "canvas") view = "split";
      }
      pathname = `/pipelines/${encodeURIComponent(matches[0].id)}/assets/${encodeURIComponent(target.asset_id)}/${view}`;
      break;
    }
    case "connection":
      pathname = "/project/connections";
      search.environment = detail.environment;
      search.connection = target.connection;
      break;
    case "data-object":
      pathname = "/data";
      break;
    case "notebook-cell":
      pathname = `/notebooks/${encodeURIComponent(target.notebook_id)}`;
      break;
    case "presentation": {
      search.presentation_editor = "visual";
      const matches =
        owners.presentations?.filter((p) => p.workspace_id === target.presentation_id) ?? [];
      if (matches.length !== 1)
        throw new Error("The linked presentation is no longer uniquely available.");
      pathname = `/${matches[0].kind === "report" ? "reports" : "dashboards"}/${encodeURIComponent(target.presentation_id)}`;
      break;
    }
  }
  return { pathname, search };
}

export function uiResourceHref(
  base: string,
  project: string,
  detail: ResourceDetail,
  owners?: NavigationOwners,
) {
  const url = new URL(resourceHref(base, project, detail));
  if (!owners) return url.href;
  const destination = resourceDestination(
    { pathname: url.pathname, search: Object.fromEntries(url.searchParams) },
    project,
    detail,
    owners,
  );
  url.pathname = destination.pathname;
  for (const key of ["editor", "presentation_editor", "environment", "connection"]) {
    const value = destination.search[key];
    if (typeof value === "string") url.searchParams.set(key, value);
  }
  return url.href;
}
