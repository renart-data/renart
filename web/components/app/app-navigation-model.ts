import {
  Activity,
  BookOpen,
  Cable,
  Calendar,
  CircleDot,
  Database,
  FileText,
  Hammer,
  History,
  LayoutDashboard,
  Network,
  Play,
  Rocket,
  Settings2,
  SlidersHorizontal,
  TerminalSquare,
  type LucideIcon,
} from "lucide-react";

export type AppModeId = "build" | "run" | "explore";
export type AppToolId =
  | "resources"
  | "ad-hoc"
  | "notebooks"
  | "data"
  | "connections"
  | "environments"
  | "pipeline-settings"
  | "project-settings"
  | "overview"
  | "deployments"
  | "schedules"
  | "runs"
  | "catalog"
  | "dashboards"
  | "reports";
export type AppSidebarKind =
  | "resources"
  | "notebooks"
  | "data"
  | "settings"
  | "overview"
  | "deployments"
  | "schedules"
  | "runs"
  | "catalog"
  | "dashboards"
  | "reports";

export type AppRouteNavigation = {
  mode: AppModeId;
  tool: AppToolId;
  sidebar: AppSidebarKind;
  mobileLabel: string;
  workbench: boolean;
};

export type AppWorkbenchTool = {
  id: AppToolId;
  mode: AppModeId;
  label: string;
  icon: LucideIcon;
  to?: string;
  position?: "primary" | "utility";
  mobileLabel?: string;
  contextual?: boolean;
};
export type AppDestinationId =
  | "workbench"
  | "notebooks"
  | "data-browser"
  | "run-overview"
  | "runs"
  | "schedules"
  | "catalog"
  | "presentations";

export type AppNavigationDestination = {
  id: AppDestinationId;
  label: string;
  to: string;
  icon: LucideIcon;
  routePrefixes: readonly string[];
};

export type AppNavigationMode = {
  id: AppModeId;
  label: string;
  to: string;
  icon: LucideIcon;
  destinations: readonly AppNavigationDestination[];
};

export const appNavigationModes = [
  {
    id: "build",
    label: "Build",
    to: "/",
    icon: Hammer,
    destinations: [
      {
        id: "workbench",
        label: "Build",
        to: "/",
        icon: Hammer,
        routePrefixes: ["/", "/pipelines"],
      },
      {
        id: "notebooks",
        label: "Notebooks",
        to: "/notebooks",
        icon: BookOpen,
        routePrefixes: ["/notebooks"],
      },
      {
        id: "data-browser",
        label: "Data Browser",
        to: "/data",
        icon: Database,
        routePrefixes: ["/data"],
      },
    ],
  },
  {
    id: "run",
    label: "Run",
    to: "/run",
    icon: Play,
    destinations: [
      {
        id: "run-overview",
        label: "Run",
        to: "/run",
        icon: Activity,
        routePrefixes: ["/run"],
      },
      {
        id: "runs",
        label: "Runs",
        to: "/runs",
        icon: Play,
        routePrefixes: ["/runs"],
      },
      {
        id: "schedules",
        label: "Schedules",
        to: "/schedules",
        icon: Calendar,
        routePrefixes: ["/schedules"],
      },
    ],
  },
  {
    id: "explore",
    label: "Explore",
    to: "/catalog",
    icon: Network,
    destinations: [
      {
        id: "catalog",
        label: "Catalog",
        to: "/catalog",
        icon: Network,
        routePrefixes: ["/catalog"],
      },
      {
        id: "presentations",
        label: "Present",
        to: "/dashboards",
        icon: LayoutDashboard,
        routePrefixes: ["/dashboards", "/reports"],
      },
    ],
  },
] as const satisfies readonly AppNavigationMode[];

export const appWorkbenchTools = {
  build: [
    {
      id: "resources",
      mode: "build",
      label: "Project resources",
      mobileLabel: "Resources",
      icon: Hammer,
      to: "/",
      contextual: true,
    },
    {
      id: "ad-hoc",
      mode: "build",
      label: "Ad-hoc query",
      mobileLabel: "Query",
      icon: TerminalSquare,
    },
    {
      id: "notebooks",
      mode: "build",
      label: "Notebooks",
      icon: BookOpen,
      to: "/notebooks",
      contextual: true,
    },
    {
      id: "data",
      mode: "build",
      label: "Data Browser",
      mobileLabel: "Data",
      icon: Database,
      to: "/data",
      contextual: true,
    },
    {
      id: "connections",
      mode: "build",
      label: "Connections",
      icon: Cable,
      to: "/project/connections",
      position: "utility",
      contextual: true,
    },
    {
      id: "environments",
      mode: "build",
      label: "Environments",
      icon: CircleDot,
      to: "/project/environments",
      position: "utility",
      contextual: true,
    },
    {
      id: "pipeline-settings",
      mode: "build",
      label: "Pipeline settings",
      mobileLabel: "Pipeline",
      icon: SlidersHorizontal,
      position: "utility",
    },
    {
      id: "project-settings",
      mode: "build",
      label: "Project settings",
      mobileLabel: "Project",
      icon: Settings2,
      to: "/project/general",
      position: "utility",
      contextual: true,
    },
  ],
  run: [
    {
      id: "overview",
      mode: "run",
      label: "Run overview",
      mobileLabel: "Overview",
      icon: Activity,
      to: "/run",
      contextual: true,
    },
    {
      id: "deployments",
      mode: "run",
      label: "Deployments",
      icon: Rocket,
      to: "/schedules/deployments",
      contextual: true,
    },
    {
      id: "schedules",
      mode: "run",
      label: "Schedules",
      icon: Calendar,
      to: "/schedules",
      contextual: true,
    },
    {
      id: "runs",
      mode: "run",
      label: "Runs",
      icon: History,
      to: "/runs",
      contextual: true,
    },
    {
      id: "project-settings",
      mode: "run",
      label: "Project settings",
      mobileLabel: "Project",
      icon: Settings2,
      to: "/project/general",
      position: "utility",
      contextual: true,
    },
  ],
  explore: [
    {
      id: "catalog",
      mode: "explore",
      label: "Workspace Catalog",
      mobileLabel: "Catalog",
      icon: Network,
      to: "/catalog",
      contextual: true,
    },
    {
      id: "dashboards",
      mode: "explore",
      label: "Dashboards",
      icon: LayoutDashboard,
      to: "/dashboards",
      contextual: true,
    },
    {
      id: "reports",
      mode: "explore",
      label: "Reports",
      icon: FileText,
      to: "/reports",
      contextual: true,
    },
    {
      id: "project-settings",
      mode: "explore",
      label: "Project settings",
      mobileLabel: "Project",
      icon: Settings2,
      to: "/project/general",
      position: "utility",
      contextual: true,
    },
  ],
} as const satisfies Record<AppModeId, readonly AppWorkbenchTool[]>;

export type AppShellRouteId =
  | "/_shell/"
  | "/_shell/data"
  | "/_shell/run"
  | "/_shell/catalog"
  | "/_shell/schedules"
  | "/_shell/runs"
  | "/_shell/project"
  | "/_shell/_presentations"
  | "/_shell/schedules/"
  | "/_shell/runs/"
  | "/_shell/project/"
  | "/_shell/notebooks/"
  | "/_shell/schedules/timeline"
  | "/_shell/schedules/deployments"
  | "/_shell/runs/$runId"
  | "/_shell/project/general"
  | "/_shell/project/environments"
  | "/_shell/project/connections"
  | "/_shell/notebooks/$notebookId"
  | "/_shell/pipelines/$pipelineId"
  | "/_shell/pipelines/$pipelineId/"
  | "/_shell/_presentations/reports/"
  | "/_shell/_presentations/dashboards/"
  | "/_shell/pipelines/$pipelineId/split"
  | "/_shell/pipelines/$pipelineId/code"
  | "/_shell/pipelines/$pipelineId/canvas"
  | "/_shell/_presentations/reports/$presentationId"
  | "/_shell/_presentations/dashboards/$presentationId"
  | "/_shell/_presentations/reports/$presentationId/"
  | "/_shell/_presentations/dashboards/$presentationId/"
  | "/_shell/_presentations/reports/$presentationId/view"
  | "/_shell/_presentations/dashboards/$presentationId/view"
  | "/_shell/pipelines/$pipelineId/assets/$assetId"
  | "/_shell/pipelines/$pipelineId/assets/$assetId/"
  | "/_shell/pipelines/$pipelineId/assets/$assetId/split"
  | "/_shell/pipelines/$pipelineId/assets/$assetId/code"
  | "/_shell/pipelines/$pipelineId/assets/$assetId/canvas";

type AppRouteNavigationDefinition = Omit<AppRouteNavigation, "workbench">;

const buildResourcesNavigation = {
  mode: "build",
  tool: "resources",
  sidebar: "resources",
  mobileLabel: "Build",
} as const satisfies AppRouteNavigationDefinition;
const buildNotebooksNavigation = {
  mode: "build",
  tool: "notebooks",
  sidebar: "notebooks",
  mobileLabel: "Notebooks",
} as const satisfies AppRouteNavigationDefinition;
const buildDataNavigation = {
  mode: "build",
  tool: "data",
  sidebar: "data",
  mobileLabel: "Data Browser",
} as const satisfies AppRouteNavigationDefinition;
const runRunsNavigation = {
  mode: "run",
  tool: "runs",
  sidebar: "runs",
  mobileLabel: "Runs",
} as const satisfies AppRouteNavigationDefinition;
const runSchedulesNavigation = {
  mode: "run",
  tool: "schedules",
  sidebar: "schedules",
  mobileLabel: "Schedules",
} as const satisfies AppRouteNavigationDefinition;
const runDeploymentsNavigation = {
  mode: "run",
  tool: "deployments",
  sidebar: "deployments",
  mobileLabel: "Deployments",
} as const satisfies AppRouteNavigationDefinition;
const runOverviewNavigation = {
  mode: "run",
  tool: "overview",
  sidebar: "overview",
  mobileLabel: "Run overview",
} as const satisfies AppRouteNavigationDefinition;
const exploreCatalogNavigation = {
  mode: "explore",
  tool: "catalog",
  sidebar: "catalog",
  mobileLabel: "Catalog",
} as const satisfies AppRouteNavigationDefinition;
const exploreDashboardsNavigation = {
  mode: "explore",
  tool: "dashboards",
  sidebar: "dashboards",
  mobileLabel: "Dashboards",
} as const satisfies AppRouteNavigationDefinition;
const exploreReportsNavigation = {
  mode: "explore",
  tool: "reports",
  sidebar: "reports",
  mobileLabel: "Reports",
} as const satisfies AppRouteNavigationDefinition;
const projectGeneralNavigation = {
  mode: "build",
  tool: "project-settings",
  sidebar: "settings",
  mobileLabel: "Project settings",
} as const satisfies AppRouteNavigationDefinition;
const projectConnectionsNavigation = {
  mode: "build",
  tool: "connections",
  sidebar: "settings",
  mobileLabel: "Connections",
} as const satisfies AppRouteNavigationDefinition;
const projectEnvironmentsNavigation = {
  mode: "build",
  tool: "environments",
  sidebar: "settings",
  mobileLabel: "Environments",
} as const satisfies AppRouteNavigationDefinition;

export const appShellRouteNavigation = {
  "/_shell/": buildResourcesNavigation,
  "/_shell/data": buildDataNavigation,
  "/_shell/run": runOverviewNavigation,
  "/_shell/catalog": exploreCatalogNavigation,
  "/_shell/schedules": runSchedulesNavigation,
  "/_shell/runs": runRunsNavigation,
  "/_shell/project": projectGeneralNavigation,
  "/_shell/_presentations": exploreDashboardsNavigation,
  "/_shell/schedules/": runSchedulesNavigation,
  "/_shell/runs/": runRunsNavigation,
  "/_shell/project/": projectGeneralNavigation,
  "/_shell/notebooks/": buildNotebooksNavigation,
  "/_shell/schedules/timeline": runOverviewNavigation,
  "/_shell/schedules/deployments": runDeploymentsNavigation,
  "/_shell/runs/$runId": runRunsNavigation,
  "/_shell/project/general": projectGeneralNavigation,
  "/_shell/project/environments": projectEnvironmentsNavigation,
  "/_shell/project/connections": projectConnectionsNavigation,
  "/_shell/notebooks/$notebookId": buildNotebooksNavigation,
  "/_shell/pipelines/$pipelineId": buildResourcesNavigation,
  "/_shell/pipelines/$pipelineId/": buildResourcesNavigation,
  "/_shell/_presentations/reports/": exploreReportsNavigation,
  "/_shell/_presentations/dashboards/": exploreDashboardsNavigation,
  "/_shell/pipelines/$pipelineId/split": buildResourcesNavigation,
  "/_shell/pipelines/$pipelineId/code": buildResourcesNavigation,
  "/_shell/pipelines/$pipelineId/canvas": buildResourcesNavigation,
  "/_shell/_presentations/reports/$presentationId": exploreReportsNavigation,
  "/_shell/_presentations/dashboards/$presentationId": exploreDashboardsNavigation,
  "/_shell/_presentations/reports/$presentationId/": exploreReportsNavigation,
  "/_shell/_presentations/dashboards/$presentationId/": exploreDashboardsNavigation,
  "/_shell/_presentations/reports/$presentationId/view": exploreReportsNavigation,
  "/_shell/_presentations/dashboards/$presentationId/view": exploreDashboardsNavigation,
  "/_shell/pipelines/$pipelineId/assets/$assetId": buildResourcesNavigation,
  "/_shell/pipelines/$pipelineId/assets/$assetId/": buildResourcesNavigation,
  "/_shell/pipelines/$pipelineId/assets/$assetId/split": buildResourcesNavigation,
  "/_shell/pipelines/$pipelineId/assets/$assetId/code": buildResourcesNavigation,
  "/_shell/pipelines/$pipelineId/assets/$assetId/canvas": buildResourcesNavigation,
} as const satisfies Record<AppShellRouteId, AppRouteNavigationDefinition>;

const migratedWorkbenchRouteIds = new Set<AppShellRouteId>([
  "/_shell/pipelines/$pipelineId",
  "/_shell/pipelines/$pipelineId/",
  "/_shell/pipelines/$pipelineId/split",
  "/_shell/pipelines/$pipelineId/code",
  "/_shell/pipelines/$pipelineId/canvas",
  "/_shell/pipelines/$pipelineId/assets/$assetId",
  "/_shell/pipelines/$pipelineId/assets/$assetId/",
  "/_shell/pipelines/$pipelineId/assets/$assetId/split",
  "/_shell/pipelines/$pipelineId/assets/$assetId/code",
  "/_shell/pipelines/$pipelineId/assets/$assetId/canvas",
  "/_shell/notebooks/",
  "/_shell/notebooks/$notebookId",
  "/_shell/data",
  "/_shell/_presentations",
  "/_shell/_presentations/reports/",
  "/_shell/_presentations/dashboards/",
  "/_shell/_presentations/reports/$presentationId",
  "/_shell/_presentations/dashboards/$presentationId",
  "/_shell/_presentations/reports/$presentationId/",
  "/_shell/_presentations/dashboards/$presentationId/",
  "/_shell/_presentations/reports/$presentationId/view",
  "/_shell/_presentations/dashboards/$presentationId/view",
  "/_shell/project",
  "/_shell/project/",
  "/_shell/project/general",
  "/_shell/project/environments",
  "/_shell/project/connections",
  "/_shell/run",
  "/_shell/catalog",
  "/_shell/schedules",
  "/_shell/schedules/",
  "/_shell/schedules/deployments",
  "/_shell/schedules/timeline",
  "/_shell/runs",
  "/_shell/runs/",
  "/_shell/runs/$runId",
]);

export function navigationForAppRouteMatches(
  matches: readonly { routeId: string }[],
): AppRouteNavigation | null {
  for (let index = matches.length - 1; index >= 0; index -= 1) {
    const routeId = matches[index]?.routeId as AppShellRouteId | undefined;
    if (!routeId) continue;
    const definition = appShellRouteNavigation[routeId];
    if (!definition) continue;
    return {
      ...definition,
      workbench: migratedWorkbenchRouteIds.has(routeId),
    };
  }
  return null;
}

function normalizePathname(pathname: string) {
  const path = pathname.split(/[?#]/, 1)[0] || "/";
  return path.length > 1 ? path.replace(/\/+$/, "") : path;
}

function routePrefixMatches(pathname: string, prefix: string) {
  if (prefix === "/") return pathname === "/";
  return pathname === prefix || pathname.startsWith(`${prefix}/`);
}

export function destinationForAppPath(pathname: string): AppNavigationDestination | null {
  const normalized = normalizePathname(pathname);
  const candidates = appNavigationModes.flatMap((mode) =>
    mode.destinations.flatMap((destination) =>
      destination.routePrefixes.map((prefix) => ({ destination, prefix })),
    ),
  );

  candidates.sort((left, right) => right.prefix.length - left.prefix.length);
  return (
    candidates.find(({ prefix }) => routePrefixMatches(normalized, prefix))?.destination ?? null
  );
}

export function modeForAppPath(pathname: string): AppNavigationMode | null {
  const destination = destinationForAppPath(pathname);
  if (!destination) return null;
  return (
    appNavigationModes.find((mode) =>
      mode.destinations.some((candidate) => candidate.id === destination.id),
    ) ?? null
  );
}
