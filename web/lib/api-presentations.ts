import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type {
  CreatePresentationRequest,
  PresentationArtifact,
  PresentationDocument,
  PresentationRunRequest,
  PresentationRunResult,
  ReplacePresentationRequest,
  UpdatePresentationRequest,
} from "@/lib/generated/api-types";

type PresentationEnvelope = {
  status: "ok";
  document: PresentationDocument;
};

export async function getPresentation(workspaceId: string) {
  const response = await fetchJSON<PresentationEnvelope>(`/api/presentations/${workspaceId}`, {
    cache: "no-store",
  });
  return response.document;
}

export async function createPresentation(input: CreatePresentationRequest) {
  const response = await fetchJSONWithBody<PresentationEnvelope>(
    "/api/presentations",
    "POST",
    input,
  );
  return response.document;
}

export async function updatePresentation(workspaceId: string, input: UpdatePresentationRequest) {
  const response = await fetchJSONWithBody<PresentationEnvelope>(
    `/api/presentations/${workspaceId}`,
    "PUT",
    input,
  );
  return response.document;
}

export async function replacePresentation(
  workspaceId: string,
  expectedRevision: string,
  artifact: PresentationArtifact,
) {
  const input: ReplacePresentationRequest = {
    expected_revision: expectedRevision,
    artifact,
  };
  const response = await fetchJSONWithBody<PresentationEnvelope>(
    `/api/presentations/${workspaceId}/definition`,
    "PUT",
    input,
  );
  return response.document;
}

export async function runPresentation(workspaceId: string, input: PresentationRunRequest) {
  return fetchJSONWithBody<PresentationRunResult>(
    `/api/presentations/${workspaceId}/run`,
    "POST",
    input,
  );
}

export type {
  PresentationArtifact,
  PresentationDataset,
  PresentationDocument,
  PresentationFilter,
  PresentationFilterBinding,
  PresentationFinding,
  PresentationRunRequest,
  PresentationRunResult,
  PresentationDatasetResult,
  PresentationLayoutItem,
  PresentationSection,
  PresentationVisualization,
} from "@/lib/generated/api-types";
