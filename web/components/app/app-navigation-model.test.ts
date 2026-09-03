import { describe, expect, it } from "vitest";

import {
  appNavigationModes,
  appShellRouteNavigation,
  appWorkbenchTools,
  destinationForAppPath,
  modeForAppPath,
  navigationForAppRouteMatches,
} from "./app-navigation-model";
import routeTreeSource from "@/src/routeTree.gen.ts?raw";

describe("app navigation model", () => {
  it("exposes only the three product modes in the primary navigation", () => {
    expect(appNavigationModes.map((mode) => mode.label)).toEqual(["Build", "Run", "Explore"]);
  });

  it("assigns every destination and default to exactly one mode", () => {
    const destinationIds = appNavigationModes.flatMap((mode) =>
      mode.destinations.map((destination) => destination.id),
    );

    expect(new Set(destinationIds).size).toBe(destinationIds.length);
    for (const mode of appNavigationModes) {
      expect(modeForAppPath(mode.to)?.id).toBe(mode.id);
    }
  });

  it.each([
    ["/", "build", "workbench"],
    ["/pipelines/cmV2ZW51ZS1tb2RlbA/split", "build", "workbench"],
    ["/notebooks/bm90ZWJvb2tzL2NoYXJ0cw", "build", "notebooks"],
    ["/data", "build", "data-browser"],
    ["/runs", "run", "runs"],
    ["/runs/123?tab=logs", "run", "runs"],
    ["/schedules/deployments", "run", "schedules"],
    ["/catalog", "explore", "catalog"],
    ["/dashboards/ZGFzaGJvYXJkcy9oZWFsdGg", "explore", "presentations"],
    ["/reports/cmVwb3J0cy93ZWVrbHk", "explore", "presentations"],
  ])("maps %s to %s/%s", (path, expectedMode, expectedDestination) => {
    expect(modeForAppPath(path)?.id).toBe(expectedMode);
    expect(destinationForAppPath(path)?.id).toBe(expectedDestination);
  });

  it.each(["/project/connections", "/welcome", "/navigation-lab/workbench", "/unknown"])(
    "keeps global or unknown route %s outside the mode registry",
    (path) => {
      expect(modeForAppPath(path)).toBeNull();
      expect(destinationForAppPath(path)).toBeNull();
    },
  );

  it("assigns every generated shell route to one exact navigation owner", () => {
    const generatedShellRouteIds = [...routeTreeSource.matchAll(/id: '(\/_shell[^']*)'/g)]
      .map((match) => match[1])
      .filter((routeId): routeId is string => Boolean(routeId) && routeId !== "/_shell")
      .sort();

    expect(Object.keys(appShellRouteNavigation).sort()).toEqual(generatedShellRouteIds);
  });

  it("uses the deepest exact route match and gates migrated workbench routes", () => {
    expect(
      navigationForAppRouteMatches([
        { routeId: "/_shell" },
        { routeId: "/_shell/runs" },
        { routeId: "/_shell/runs/$runId" },
      ]),
    ).toMatchObject({ mode: "run", tool: "runs", workbench: true });
    expect(
      navigationForAppRouteMatches([
        { routeId: "/_shell" },
        { routeId: "/_shell/pipelines/$pipelineId" },
      ]),
    ).toMatchObject({ mode: "build", tool: "resources", workbench: true });
  });

  it("keeps tool ids unique inside each mode", () => {
    for (const tools of Object.values(appWorkbenchTools)) {
      expect(new Set(tools.map((tool) => tool.id)).size).toBe(tools.length);
    }
  });

  it("exposes the data browser as a Build route and contextual tool", () => {
    const dataTool = appWorkbenchTools.build.find((tool) => tool.id === "data");
    expect(dataTool).toMatchObject({ to: "/data", contextual: true });
  });
});
