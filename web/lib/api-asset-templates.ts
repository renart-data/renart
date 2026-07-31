export type APIAssetTemplateId = "openapi" | "paginated" | "request-body" | "pipedrive";

export type APIAssetTemplate = {
  id: APIAssetTemplateId;
  label: string;
  description: string;
};

export const API_ASSET_TEMPLATES: APIAssetTemplate[] = [
  {
    id: "openapi",
    label: "OpenAPI spec",
    description: "Start from your own spec, then choose an endpoint with editor suggestions.",
  },
  {
    id: "paginated",
    label: "Paginated REST",
    description: "PokéAPI records with body-provided next-page URLs and no credentials.",
  },
  {
    id: "request-body",
    label: "JSON request body",
    description: "A POST request with a nested JSON body and no credentials.",
  },
  {
    id: "pipedrive",
    label: "Pipedrive Deals",
    description: "API-key auth, cursor pagination, execution-window filtering, and merge.",
  },
];

export function buildAPIAssetTemplate(
  templateId: APIAssetTemplateId,
  connection?: string,
  openapiURL?: string,
): string {
  const connectionLine = connection?.trim() ? `connection: ${connection.trim()}\n` : "";

  if (templateId === "paginated") {
    return `type: api
${connectionLine}
materialization:
  type: table
  strategy: create+replace

parameters:
  request:
    url: https://pokeapi.co/api/v2/pokemon?limit=100
    method: GET
    headers:
      Accept: application/json

  response:
    records_path: results

  pagination:
    type: next_url
    next_url_path: next
    max_pages: 20
`;
  }

  if (templateId === "pipedrive") {
    return `type: api
${connectionLine}
materialization:
  type: table
  strategy: merge

parameters:
  request:
    url: https://{{ env.PIPEDRIVE_COMPANY_DOMAIN }}.pipedrive.com/api/v2/deals
    method: GET
    params:
      limit: 500
      sort_by: update_time
      sort_direction: asc
      updated_since: "{{ start_timestamp }}"

  auth:
    type: api_key
    name: api_token
    value: "{{ env.PIPEDRIVE_API_TOKEN }}"
    in: query

  response:
    records_path: data

  pagination:
    type: cursor
    cursor_param: cursor
    cursor_path: additional_data.next_cursor
    limit_param: limit
    limit: 500
    max_pages: 100

columns:
  - name: id
    type: integer
    primary_key: true
  - name: update_time
    type: timestamp
`;
  }

  if (templateId === "request-body") {
    return `type: api
${connectionLine}
materialization:
  type: table
  strategy: create+replace

parameters:
  request:
    url: https://httpbin.org/anything
    method: POST
    headers:
      Accept: application/json
      Content-Type: application/json
    body:
      event: pipeline_preview
      source:
        application: renart
        environment: local
      tags:
        - http-api
        - request-body

  response:
    records_path: json
`;
  }

  return `type: api
${connectionLine}
materialization:
  type: table
  strategy: create+replace

parameters:
  openapi:
    url: ${JSON.stringify(openapiURL?.trim() ?? "")}
    validation: warn

  request:
    url: ""
    method: GET
    headers:
      Accept: application/json
      User-Agent: Renart/alpha (https://getrenart.com)

  response:
    records_path: ""
`;
}
