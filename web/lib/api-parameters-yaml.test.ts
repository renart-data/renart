import { describe, expect, it } from "vitest";

import {
  extractParametersText,
  findParametersBlock,
  spliceParametersText,
} from "@/lib/api-parameters-yaml";

describe("API parameters YAML editing", () => {
  it("stops at the next genuine top-level YAML key", () => {
    const content = `type: api
parameters:
  request:
    url: https://example.test

columns:
  - name: id
`;

    expect(extractParametersText(content)).toBe(`parameters:
  request:
    url: https://example.test`);
    expect(findParametersBlock(content).end).toBe(4);
  });

  it("keeps malformed pasted JSON visible until it can be repaired", () => {
    const content = `type: api
parameters:
  request:
    body:
{
  "customer": {
    "id": 42
  }
}
columns:
  - name: id
`;

    const block = extractParametersText(content);
    expect(block).toContain(`{\n  "customer": {`);
    expect(block).toContain("\n  }\n}");

    const repaired = `parameters:
  request:
    body:
      customer:
        id: 42`;
    expect(spliceParametersText(content, repaired)).toBe(`type: api
${repaired}
columns:
  - name: id
`);
  });
});
