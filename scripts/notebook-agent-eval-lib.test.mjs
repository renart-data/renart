import assert from "node:assert/strict";
import test from "node:test";

import {
  evaluateObjectives,
  findSecretLeaks,
  normalizeToolName,
  redactKnownSecrets,
  selectQuestionnaireAnswers,
} from "./notebook-agent-eval-lib.mjs";

test("normalizes native provider activity names", () => {
  assert.equal(normalizeToolName("renart_get_notebook_outline"), "get_notebook_outline");
  assert.equal(
    normalizeToolName("mcp__renart__prepare_notebook_change_set"),
    "prepare_notebook_change_set",
  );
});

test("maps a preferred answer across model-chosen questionnaire IDs", () => {
  const answers = selectQuestionnaireAnswers(
    {
      questions: [
        {
          id: "primary_measure",
          kind: "single_choice",
          options: [
            { value: "gross_sales", label: "Revenue" },
            { value: "orders", label: "Order count", recommended: true },
          ],
        },
      ],
    },
    { answers: { metric: ["revenue"] } },
  );
  assert.deepEqual(answers, [{ question_id: "primary_measure", values: ["gross_sales"] }]);
});

test("detects canaries before redacting report artifacts", () => {
  const canary = "renart-eval-secret-canary";
  assert.deepEqual(findSecretLeaks(`value=${canary}`, [canary]), [
    { kind: "known_secret", fingerprint: `len:${canary.length}` },
  ]);
  assert.equal(redactKnownSecrets(`value=${canary}`, [canary]), "value=[REDACTED]");
});

test("scores deterministic tool, file, and notebook objectives", () => {
  const task = {
    notebook_path: "notebooks/example",
    objectives: [
      { kind: "terminal_status", equals: "idle" },
      { kind: "tool_called", tool: "get_notebook_outline" },
      { kind: "file_contains", path: "notebooks/example/query.sql", text: "select" },
      {
        kind: "directory_contains_any",
        path: "notebooks/example",
        texts: ["retail.order_history", "retail.daily_sales"],
      },
      { kind: "notebook_block_kind", block_kind: "cell", minimum: 1 },
    ],
  };
  const results = evaluateObjectives(task, {
    finalSnapshot: { status: "idle" },
    toolCounts: { get_notebook_outline: 1 },
    interactions: [],
    changedPaths: ["notebooks/example/query.sql"],
    initialHead: "a",
    finalHead: "a",
    secretLeaks: [],
    files: { "notebooks/example/query.sql": "select 1" },
    directories: { "notebooks/example": "select * from retail.daily_sales" },
    finalNotebook: { blocks: [{ cell: "query" }] },
    finalDiagnostics: { diagnostics: [] },
    finalRuntime: { results: {} },
  });
  assert.ok(results.every((result) => result.passed), JSON.stringify(results));
});
