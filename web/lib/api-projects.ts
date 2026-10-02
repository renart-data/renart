import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type {
  BrowseDirsResponse,
  CreateDirectoryResponse,
  CreateProjectRequest,
  CreateProjectResponse,
  DuckDBDriverResponse,
  OpenProjectResponse,
  ProjectListResponse,
  ProjectTemplatesResponse,
} from "@/lib/generated/api-types";

// These endpoints are process-level (the project directory itself), so they
// are never rewritten onto a project mount by projectApiPath.

export async function listProjects(): Promise<ProjectListResponse> {
  return fetchJSON<ProjectListResponse>("/api/projects", { cache: "no-store" });
}

export async function openProject(path: string): Promise<OpenProjectResponse> {
  return fetchJSONWithBody<OpenProjectResponse>("/api/projects/open", "POST", { path });
}

export async function getProjectTemplates(): Promise<ProjectTemplatesResponse> {
  return fetchJSON<ProjectTemplatesResponse>("/api/projects/templates", { cache: "no-store" });
}

// DuckDB's driver is machine-wide and downloaded on first use; the first run
// prepares it as a visible step.
export async function getDuckDBDriver(): Promise<DuckDBDriverResponse> {
  return fetchJSON<DuckDBDriverResponse>("/api/projects/duckdb-driver", { cache: "no-store" });
}

export async function prepareDuckDBDriver(): Promise<DuckDBDriverResponse> {
  return fetchJSONWithBody<DuckDBDriverResponse>("/api/projects/duckdb-driver", "POST");
}

export async function createProject(input: CreateProjectRequest): Promise<CreateProjectResponse> {
  return fetchJSONWithBody<CreateProjectResponse>("/api/projects", "POST", input);
}

export async function removeProject(id: string): Promise<ProjectListResponse> {
  return fetchJSONWithBody<ProjectListResponse>(
    `/api/projects/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export async function browseProjectDirs(
  path?: string,
  purpose?: "create",
): Promise<BrowseDirsResponse> {
  const search = new URLSearchParams();
  if (path) search.set("path", path);
  if (purpose) search.set("purpose", purpose);
  const query = search.toString();
  return fetchJSON<BrowseDirsResponse>(`/api/projects/browse${query ? `?${query}` : ""}`, {
    cache: "no-store",
  });
}

export async function createProjectDirectory(
  parentDir: string,
  name: string,
): Promise<CreateDirectoryResponse> {
  return fetchJSONWithBody<CreateDirectoryResponse>("/api/projects/directories", "POST", {
    parent_dir: parentDir,
    name,
  });
}
