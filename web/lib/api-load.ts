import { buildQueryString, fetchJSON } from "@/lib/api-core";

export type LoadDiscoveryStream = {
  name: string;
  schema?: string;
};

export type LoadDiscoveryResponse = {
  status: string;
  connection_name: string;
  pattern?: string;
  streams: LoadDiscoveryStream[];
  truncated?: boolean;
  error?: string;
};

// discoverLoadStreams lists the objects/streams a bruin connection exposes
// (via `sling conns discover`) for source/target intellisense in the editor.
export async function discoverLoadStreams(options: {
  connection: string;
  pattern?: string;
  environment?: string;
  signal?: AbortSignal;
}) {
  return fetchJSON<LoadDiscoveryResponse>(
    `/api/load/discover${buildQueryString({
      connection: options.connection,
      pattern: options.pattern,
      environment: options.environment,
    })}`,
    { cache: "no-store", signal: options.signal },
  );
}
