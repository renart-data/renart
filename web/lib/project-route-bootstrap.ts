import { listProjects } from "./api-projects";
import { pinProject } from "./project-context";
import type { ProjectListResponse } from "./generated/api-types";

export function validateProjectLink(id: string, directory: ProjectListResponse) {
  if (!directory.projects.some((project) => project.id === id && project.exists)) {
    throw new Error(
      "The project in this link is unavailable. Open the project in Renart and try again.",
    );
  }
}

let pending: Promise<void> | undefined;
let scopedProject: string | undefined;

// Legacy URLs initially use the existing session/default API mount. Once the
// server identifies that workspace, lock the same scope for subsequent links.
export function rememberWorkspaceProject(project: string) {
  if (!scopedProject) scopedProject = project;
}

// Root beforeLoad completes before the shell, workspace effects and SSE mount.
// A running app has one API/cache scope: switching projects requires a document
// navigation, as it already does in ProjectSwitcher. Never retarget live drafts.
export async function bootstrapProjectRoute(project: string | undefined) {
  if (!project) return;
  if (scopedProject === project) return pending;
  if (scopedProject)
    throw new Error("Open this project link in a new tab to keep your current workspace intact.");
  scopedProject = project;
  pending = listProjects()
    .then((directory) => {
      validateProjectLink(project, directory);
      pinProject(project);
    })
    .catch((error: unknown) => {
      scopedProject = undefined;
      pending = undefined;
      throw error;
    });
  return pending;
}
