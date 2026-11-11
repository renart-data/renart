import { projectApiPath } from "@/lib/project-context";
import { AssetInspectResponse, OperationMetadata } from "@/lib/types";
import { extractInspectErrorText } from "@/lib/inspect-errors";

export type JSONErrorPayload = {
  error?:
    | {
        code?: string;
        message?: string;
        details?: unknown;
      }
    | string;
  message?: string;
};

export class APIError extends Error {
  readonly status: number;
  readonly code: string | null;
  readonly details: unknown;

  constructor(input: { status: number; message: string; code?: string | null; details?: unknown }) {
    super(input.message);
    this.name = "APIError";
    this.status = input.status;
    this.code = input.code ?? null;
    this.details = input.details;
  }
}

export type MaterializeStreamPayload = {
  status?: "ok" | "error";
  operation?: OperationMetadata;
  output?: string;
  error?: string;
  exit_code?: number;
  changed_asset_ids?: string[];
  materialized_at?: string;
  warnings?: string[];
  chunk?: string;
  run_scope?: string;
};

export type FillColumnsFromDBResponse = {
  status: "ok" | "error";
  results?: Array<{
    operation: OperationMetadata;
    output: string;
    exit_code: number;
    error?: string;
  }>;
};

export async function readJSON<T>(res: Response): Promise<T> {
  if (!res.ok) {
    throw await readResponseError(res);
  }

  return (await res.json()) as T;
}

export async function getResponseErrorMessage(res: Response): Promise<string> {
  return (await readResponseError(res)).message;
}

export async function readResponseError(res: Response): Promise<APIError> {
  const text = await res.text();
  const parsed = parseJSONSafely<JSONErrorPayload>(text);
  const error = typeof parsed?.error === "object" ? parsed.error : null;
  const message =
    error?.message ||
    (typeof parsed?.error === "string" ? parsed.error : undefined) ||
    parsed?.message ||
    text ||
    `Request failed: ${res.status}`;

  if (res.status === 401 && error?.code === "authentication_required") {
    redirectToSignIn();
  }

  return new APIError({
    status: res.status,
    message,
    code: error?.code,
    details: error?.details,
  });
}

let signInRedirectStarted = false;

// redirectToSignIn sends the browser to the server's sign-in page once, so an
// expired access cookie does not leave the app showing request errors.
function redirectToSignIn() {
  if (signInRedirectStarted || typeof window === "undefined") {
    return;
  }
  signInRedirectStarted = true;
  const next = `${window.location.pathname}${window.location.search}`;
  window.location.assign(`/auth?next=${encodeURIComponent(next)}`);
}

export function parseJSONSafely<T>(text: string): T | null {
  try {
    return JSON.parse(text) as T;
  } catch {
    return null;
  }
}

export function buildQueryString(params: Record<string, string | number | undefined>) {
  const searchParams = new URLSearchParams();

  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === "") {
      continue;
    }

    searchParams.set(key, String(value));
  }

  const query = searchParams.toString();
  return query ? `?${query}` : "";
}

// resolveInput routes string /api paths through the per-tab project pin so
// every caller of these helpers targets the right project runtime.
function resolveInput(input: RequestInfo | URL): RequestInfo | URL {
  return typeof input === "string" ? projectApiPath(input) : input;
}

export async function fetchJSON<T>(input: RequestInfo | URL, init?: RequestInit) {
  const res = await fetch(resolveInput(input), init);
  return readJSON<T>(res);
}

export async function fetchJSONWithBody<T>(
  input: RequestInfo | URL,
  method: "POST" | "PUT" | "DELETE",
  body?: unknown,
  init?: RequestInit,
) {
  return fetchJSON<T>(input, {
    ...init,
    method,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export async function readTextOrThrow(res: Response) {
  if (!res.ok) {
    throw await readResponseError(res);
  }

  return res.text();
}

export async function fetchText(input: RequestInfo | URL, init?: RequestInit) {
  const res = await fetch(resolveInput(input), init);
  const text = await readTextOrThrow(res);

  return { res, text };
}

export async function fetchParsedText<T>(input: RequestInfo | URL, init?: RequestInit) {
  const { res, text } = await fetchText(input, init);

  return {
    res,
    text,
    parsed: parseJSONSafely<T>(text),
  };
}

export function normalizeInspectResponse(response: AssetInspectResponse): AssetInspectResponse {
  if (response.status !== "error") {
    return response;
  }

  const extracted = extractInspectErrorText(response.raw_output);

  if (!extracted) {
    return response;
  }

  return {
    ...response,
    error: extracted,
  };
}
