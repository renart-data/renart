import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ProjectListResponse } from "./generated/api-types";

const directory = {
  projects: [
    { id: "a", exists: true },
    { id: "gone", exists: false },
  ],
} as ProjectListResponse;
const listProjects = vi.fn();
const pinProject = vi.fn();
vi.mock("./api-projects", () => ({ listProjects: (...args: unknown[]) => listProjects(...args) }));
vi.mock("./project-context", () => ({ pinProject: (...args: unknown[]) => pinProject(...args) }));

describe("project route bootstrap", () => {
  beforeEach(() => {
    vi.resetModules();
    listProjects.mockReset();
    pinProject.mockReset();
  });
  it("validates before pinning, shares in-flight bootstrap, and never repins on detail navigation", async () => {
    const { bootstrapProjectRoute } = await import("./project-route-bootstrap");
    let resolve!: (value: ProjectListResponse) => void;
    listProjects.mockReturnValue(
      new Promise<ProjectListResponse>((done) => {
        resolve = done;
      }),
    );
    const first = bootstrapProjectRoute("a");
    const second = bootstrapProjectRoute("a");
    expect(pinProject).not.toHaveBeenCalled();
    resolve(directory);
    await Promise.all([first, second]);
    await bootstrapProjectRoute("a");
    expect(listProjects).toHaveBeenCalledTimes(1);
    expect(pinProject).toHaveBeenCalledExactlyOnceWith("a");
    await expect(bootstrapProjectRoute("b")).rejects.toThrow(/new tab/);
    expect(pinProject).toHaveBeenCalledTimes(1);
  });
  it("never falls back to default for invalid, missing, or failed projects", async () => {
    const { bootstrapProjectRoute } = await import("./project-route-bootstrap");
    listProjects.mockResolvedValue(directory);
    await expect(bootstrapProjectRoute("missing")).rejects.toThrow(/unavailable/);
    await expect(bootstrapProjectRoute("gone")).rejects.toThrow(/unavailable/);
    listProjects.mockRejectedValue(new Error("offline"));
    await expect(bootstrapProjectRoute("a")).rejects.toThrow("offline");
    expect(pinProject).not.toHaveBeenCalled();
    await bootstrapProjectRoute(undefined);
  });
  it("locks a loaded legacy workspace before the first explicit detail link", async () => {
    const { bootstrapProjectRoute, rememberWorkspaceProject } =
      await import("./project-route-bootstrap");
    rememberWorkspaceProject("a");
    await bootstrapProjectRoute("a");
    await expect(bootstrapProjectRoute("b")).rejects.toThrow(/new tab/);
    expect(listProjects).not.toHaveBeenCalled();
    expect(pinProject).not.toHaveBeenCalled();
  });
});
