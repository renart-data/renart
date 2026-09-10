import type { WorkspaceConfigEnvironment } from "./types";

export type SettingsSearch = {
  environment?: string;
  connection?: string;
  action?: "create" | "clone" | "vault";
};

export function normalizeSettingsSearch(
  search: Record<string, unknown>,
  section: "connections" | "environments",
): SettingsSearch {
  return {
    environment:
      typeof search.environment === "string" && search.environment ? search.environment : undefined,
    connection:
      section === "connections" && typeof search.connection === "string" && search.connection
        ? search.connection
        : undefined,
    action:
      search.action === "create" ||
      (section === "environments" && search.action === "clone") ||
      (section === "connections" && search.action === "vault")
        ? search.action
        : undefined,
  };
}

// Field locators and independent workbench panels do not identify a new draft.
export function settingsEditorIdentity(
  location: {
    pathname: string;
    search: Record<string, unknown>;
  },
  project?: string,
) {
  const search = location.search;
  const detail = search.detail as
    | { environment?: string; target?: { kind?: string; connection?: string } }
    | undefined;
  const connectionDetail =
    location.pathname === "/project/connections" && detail?.target?.kind === "connection"
      ? detail
      : undefined;
  const connection = connectionDetail?.target?.connection ?? search.connection;
  return JSON.stringify([
    search.project ?? project,
    location.pathname,
    connectionDetail?.environment ?? search.environment,
    connection,
    connection ? undefined : search.action,
  ]);
}

export function filterSettingsEnvironments(
  environments: WorkspaceConfigEnvironment[],
  filter: string,
  selected?: { environment?: string; connection?: string },
) {
  const query = filter.trim().toLowerCase();
  return environments.flatMap((environment) => {
    if (!query || environment.name.toLowerCase().includes(query)) return [environment];
    const connections = environment.connections.filter(
      (connection) =>
        `${connection.name} ${connection.type}`.toLowerCase().includes(query) ||
        (environment.name === selected?.environment && connection.name === selected.connection),
    );
    return connections.length ||
      (environment.name === selected?.environment && !selected.connection)
      ? [{ ...environment, connections }]
      : [];
  });
}
