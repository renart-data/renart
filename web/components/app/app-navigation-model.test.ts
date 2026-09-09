import { describe, expect, it } from "vitest";

import {
  appNavigationModes,
  appShellRouteNavigation,
  appWorkbenchTools,
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
      const owner = Object.entries(appShellRouteNavigation).find(
        ([routeId]) => routeId === `/_shell${mode.to}`,
      )?.[1];
      expect(owner?.mode).toBe(mode.id);
    }
  });

  it("assigns every generated shell route to one exact navigation owner", () => {
    const generatedShellRouteIds = [...routeTreeSource.matchAll(/id: '(\/_shell[^']*)'/g)]
      .map((match) => match[1])
      .filter((routeId): routeId is string => Boolean(routeId) && routeId !== "/_shell")
      .sort();

    expect(Object.keys(appShellRouteNavigation).sort()).toEqual(generatedShellRouteIds);
  });

  it("retires the navigation study without removing the semantic impact playground", () => {
    expect(routeTreeSource).not.toContain("/navigation-lab");
    expect(routeTreeSource).toContain("/semantic-diff");
  });

  it("uses the deepest exact route match", () => {
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

  it("uses one workbench for every shell page except the root redirect", () => {
    for (const routeId of Object.keys(appShellRouteNavigation)) {
      expect(navigationForAppRouteMatches([{ routeId }])?.workbench).toBe(routeId !== "/_shell/");
    }
    expect(navigationForAppRouteMatches([{ routeId: "/unknown" }])).toBeNull();
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
