import { afterEach, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllGlobals();
});

it("keeps the validated runtime project when browser storage is unavailable", async () => {
  vi.resetModules();
  const fail = () => {
    throw new Error("storage unavailable");
  };
  vi.stubGlobal("window", { sessionStorage: { getItem: fail, setItem: fail, removeItem: fail } });
  const { pinProject, getPinnedProjectId, projectApiPath } = await import("./project-context");
  expect(getPinnedProjectId()).toBeNull();
  pinProject("project ä");
  expect(getPinnedProjectId()).toBe("project ä");
  expect(projectApiPath("/api/workspace")).toBe("/api/projects/project%20%C3%A4/workspace");
  expect(projectApiPath("/api/events")).toBe("/api/projects/project%20%C3%A4/events");
  expect(projectApiPath("/api/projects")).toBe("/api/projects");
  expect(projectApiPath("/api/projects/open")).toBe("/api/projects/open");
  pinProject(null);
  expect(projectApiPath("/api/workspace")).toBe("/api/workspace");
});
