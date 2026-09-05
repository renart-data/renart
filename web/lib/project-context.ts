// Per-tab project pin. sessionStorage is per-tab, so two tabs can work on
// different projects against one server without fighting each other. No pin
// means the server's default project, reached via the unprefixed /api
// routes (the migration alias).
const STORAGE_KEY = "renart.project";
let runtimePin: string | null | undefined;

export function getPinnedProjectId(): string | null {
  if (runtimePin !== undefined) return runtimePin;
  if (typeof window === "undefined") return null;
  try {
    return window.sessionStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export function pinProject(projectId: string | null) {
  runtimePin = projectId;
  if (typeof window === "undefined") return;
  try {
    if (projectId) {
      window.sessionStorage.setItem(STORAGE_KEY, projectId);
    } else {
      window.sessionStorage.removeItem(STORAGE_KEY);
    }
  } catch {
    // The validated runtime scope still works when storage is unavailable.
  }
}

// projectApiPath rewrites /api/... to the pinned project's mount,
// /api/projects/{id}/... — the single place the project segment enters
// request URLs. Process-level routes (the project directory itself) stay
// unprefixed.
export function projectApiPath(path: string): string {
  const pinned = getPinnedProjectId();
  if (!pinned) return path;
  if (!path.startsWith("/api/") || path.startsWith("/api/projects")) return path;
  return `/api/projects/${encodeURIComponent(pinned)}${path.slice(4)}`;
}
