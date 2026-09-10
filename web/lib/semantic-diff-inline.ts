import type { SemanticDiffDemoScenario } from "./semantic-diff-demo";

export type SemanticDiffInlineTone = "safe" | "warning";

export type SemanticDiffInlineAnchor = {
  lineNumber: number;
  startColumn: number;
  endColumn: number;
  label: string;
};

export type SemanticDiffInlineFinding = {
  id: string;
  title: string;
  detail: string;
  tone: SemanticDiffInlineTone;
  before?: SemanticDiffInlineAnchor;
  after?: SemanticDiffInlineAnchor;
};

export type SemanticDiffDraftAnalysis = {
  tone: SemanticDiffInlineTone;
  canonicalSame: boolean;
  matchesSavedCandidate: boolean;
  findings: SemanticDiffInlineFinding[];
};

const sqlTokenPattern =
  /'(?:''|[^'])*'|"(?:""|[^"])*"|`(?:``|[^`])*`|[A-Za-z_][\w$]*|\d+(?:\.\d+)?|<>|!=|<=|>=|::|[(),.*+\-/=<>;]/g;

export function canonicalizeDemoSql(sql: string) {
  const withoutComments = sql.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/--[^\r\n]*/g, " ");
  const tokens = withoutComments.match(sqlTokenPattern) ?? [];

  return tokens
    .filter((token) => token !== ";")
    .map((token) => (token.startsWith("'") ? token : token.toLowerCase()))
    .join(" ");
}

export function analyzeSemanticDiffDraft(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
): SemanticDiffDraftAnalysis {
  const canonicalSame = canonicalizeDemoSql(scenario.before.sql) === canonicalizeDemoSql(draftSql);
  const matchesSavedCandidate = scenario.after.sql === draftSql;
  let findings: SemanticDiffInlineFinding[];

  switch (scenario.id) {
    case "propagated-type":
      findings = analyzePropagatedType(scenario, draftSql);
      break;
    case "formatting-only":
      findings = analyzeFormatting(scenario, draftSql, canonicalSame);
      break;
    case "behavior-change":
      findings = analyzeBehavior(scenario, draftSql, canonicalSame);
      break;
    case "contract-break":
      findings = analyzeContract(scenario, draftSql, canonicalSame);
      break;
  }

  return {
    tone: findings.some((finding) => finding.tone === "warning") ? "warning" : "safe",
    canonicalSame,
    matchesSavedCandidate,
    findings,
  };
}

function analyzePropagatedType(scenario: SemanticDiffDemoScenario, draftSql: string) {
  const before = anchorForToken(scenario.before.sql, "total_amount", "HUGEINT");
  const after = anchorForToken(draftSql, "total_amount", "DOUBLE ↑");

  if (!after) {
    return [
      genericDraftFinding(
        draftSql,
        "Saved lineage no longer applies",
        "The edited query no longer references total_amount, so the curated upstream type trace cannot be attached.",
      ),
    ];
  }

  const containedTypeAnchor = explicitHugeintSumCastAnchor(draftSql);
  if (containedTypeAnchor) {
    return [
      {
        id: "propagated-type-contained",
        title: "Output contract pinned",
        detail:
          "lineitems.total_amount still widens to DOUBLE, but the explicit HUGEINT cast preserves the deployed output boundary for known consumers.",
        tone: "safe" as const,
        before,
        after: containedTypeAnchor,
      },
    ];
  }

  const findings: SemanticDiffInlineFinding[] = [
    {
      id: "propagated-total-amount-type",
      title: "Upstream type propagated",
      detail:
        "The SQL expression is unchanged, but lineitems.total_amount widens from INTEGER to DOUBLE and changes SUM(total_amount) from HUGEINT to DOUBLE.",
      tone: "warning",
      before,
      after,
    },
  ];

  if (canonicalizeDemoSql(draftSql) !== canonicalizeDemoSql(scenario.after.sql)) {
    findings.push(
      genericDraftFinding(
        draftSql,
        "Candidate query also changed",
        "The inline type trace is still attached, but a production analyzer would recompute the complete graph for this draft.",
      ),
    );
  }

  return findings;
}

function explicitHugeintSumCastAnchor(sql: string) {
  const expression = /\bcast\s*\(\s*sum\s*\(\s*total_amount\s*\)\s+as\s+(hugeint)\s*\)/i.exec(sql);
  if (!expression || expression.index === undefined || !expression[1]) return undefined;
  const typeOffset = expression[0].toLowerCase().lastIndexOf(expression[1].toLowerCase());

  return anchorAtIndex(sql, expression.index + typeOffset, expression[1].length, "HUGEINT ✓");
}

function analyzeFormatting(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  canonicalSame: boolean,
) {
  if (!canonicalSame) {
    return [
      genericDraftFinding(
        draftSql,
        "Query semantics changed",
        "After comments and formatting are normalized, the candidate no longer matches the deployed query.",
        "query-semantics-changed",
      ),
    ];
  }

  return [
    {
      id: "formatting-only",
      title: "Formatting only",
      detail:
        "Comments, casing, spacing, and line breaks differ; the canonical query and inferred output contract are unchanged.",
      tone: "safe" as const,
      before: anchorForToken(scenario.before.sql, "SELECT", "canonical query"),
      after: anchorForToken(draftSql, "select", "same canonical query"),
    },
  ];
}

function analyzeBehavior(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  canonicalSame: boolean,
) {
  const beforeValues = extractStatusValues(scenario.before.sql);
  const afterValues = extractStatusValues(draftSql);
  const beforeAnchor = statusAnchor(scenario.before.sql, valuesLabel(beforeValues));

  if (afterValues.length === 0) {
    return [
      {
        id: "status-filter-change",
        title: "Status filter removed",
        detail:
          "No status predicate can be found in the edited candidate, widening the eligible row set.",
        tone: "warning" as const,
        before: beforeAnchor,
        after: statementAnchor(draftSql, "status filter missing · wider row set"),
      },
    ];
  }

  const added = afterValues.filter((value) => !beforeValues.includes(value));
  const removed = beforeValues.filter((value) => !afterValues.includes(value));
  const sameValues = added.length === 0 && removed.length === 0;

  if (sameValues && canonicalSame) {
    return [
      {
        id: "status-filter-restored",
        title: "Filter restored",
        detail: "The candidate once again selects only the deployed status population.",
        tone: "safe" as const,
        before: beforeAnchor,
        after: statusAnchor(draftSql, "same row population"),
      },
    ];
  }

  if (sameValues) {
    return [
      genericDraftFinding(
        draftSql,
        "Query changed outside the filter",
        "The status population is stable, but another canonical query component changed.",
      ),
    ];
  }

  const widened = removed.length === 0 && added.length > 0;
  const narrowed = added.length === 0 && removed.length > 0;
  const title = widened ? "Filter widened" : narrowed ? "Filter narrowed" : "Filter changed";
  const label = widened
    ? `+ ${added.join(" + ")}`
    : narrowed
      ? `− ${removed.join(" − ")}`
      : `${valuesLabel(beforeValues)} → ${valuesLabel(afterValues)}`;

  return [
    {
      id: "status-filter-change",
      title,
      detail: `The status predicate changes from ${valuesLabel(beforeValues)} to ${valuesLabel(afterValues)} while the output schema stays stable.`,
      tone: "warning" as const,
      before: beforeAnchor,
      after: statusAnchor(draftSql, label),
    },
  ];
}

function analyzeContract(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  canonicalSame: boolean,
) {
  const beforeAnchor = anchorForToken(scenario.before.sql, "currency", "VARCHAR");
  const currencyAnchor = selectColumnAnchor(draftSql, "currency", "currency ✓");

  if (!currencyAnchor) {
    return [
      {
        id: "removed-output-currency",
        title: "Output column removed",
        detail:
          "Position 3 (currency VARCHAR) disappears from the ordered output contract even though the SQL remains executable.",
        tone: "warning" as const,
        before: beforeAnchor,
        after: lastProjectionAnchor(draftSql, "− currency"),
      },
    ];
  }

  const findings: SemanticDiffInlineFinding[] = [
    {
      id: "output-contract-restored",
      title: "Output contract restored",
      detail: "currency is present again at output position 3, matching the deployed contract.",
      tone: "safe",
      before: beforeAnchor,
      after: currencyAnchor,
    },
  ];

  if (!canonicalSame) {
    findings.push(
      genericDraftFinding(
        draftSql,
        "Candidate query also changed",
        "The removed column is restored, but another canonical query component still differs from deployment.",
      ),
    );
  }

  return findings;
}

function extractStatusValues(sql: string) {
  const where = sql.match(
    /\bwhere\b([\s\S]*?)(?:\bgroup\s+by\b|\border\s+by\b|\bqualify\b|\bhaving\b|\blimit\b|$)/i,
  )?.[1];
  if (!where || !/\bstatus\b/i.test(where)) return [];

  return Array.from(where.matchAll(/'((?:''|[^'])*)'/g), (match) =>
    match[1].replace(/''/g, "'").toLowerCase(),
  );
}

function valuesLabel(values: string[]) {
  if (values.length === 0) return "no statuses";
  if (values.length === 1) return `${values[0]} only`;
  return values.join(" + ");
}

function statusAnchor(sql: string, label: string) {
  return anchorForToken(sql, "status", label) ?? statementAnchor(sql, label);
}

function selectColumnAnchor(sql: string, column: string, label: string) {
  const clause = selectClause(sql);
  if (!clause || !new RegExp(`\\b${escapeRegExp(column)}\\b`, "i").test(clause.text)) {
    return undefined;
  }

  return anchorForToken(sql, column, label, clause.start);
}

function lastProjectionAnchor(sql: string, label: string) {
  const clause = selectClause(sql);
  if (!clause) return statementAnchor(sql, label);

  const tokens = Array.from(clause.text.matchAll(/[A-Za-z_][\w$]*/g));
  const token = tokens.at(-1);
  if (!token || token.index === undefined) return statementAnchor(sql, label);

  return anchorAtIndex(sql, clause.start + token.index, token[0].length, label);
}

function selectClause(sql: string) {
  const match = /\bselect\b([\s\S]*?)\bfrom\b/i.exec(sql);
  if (!match || match.index === undefined) return undefined;
  const selectKeywordEnd = match.index + match[0].toLowerCase().indexOf(match[1].toLowerCase());

  return { text: match[1], start: selectKeywordEnd };
}

function genericDraftFinding(
  sql: string,
  title: string,
  detail: string,
  id = "candidate-query-changed",
): SemanticDiffInlineFinding {
  return {
    id,
    title,
    detail,
    tone: "warning",
    after: statementAnchor(sql, "semantic re-analysis required"),
  };
}

function statementAnchor(sql: string, label: string) {
  const token = /[A-Za-z_][\w$]*/.exec(sql);
  if (!token || token.index === undefined) {
    return { lineNumber: 1, startColumn: 1, endColumn: 1, label };
  }

  return anchorAtIndex(sql, token.index, token[0].length, label);
}

function anchorForToken(
  sql: string,
  token: string,
  label: string,
  startAt = 0,
): SemanticDiffInlineAnchor | undefined {
  const match = new RegExp(`\\b${escapeRegExp(token)}\\b`, "i").exec(sql.slice(startAt));
  if (!match || match.index === undefined) return undefined;

  return anchorAtIndex(sql, startAt + match.index, match[0].length, label);
}

function anchorAtIndex(sql: string, index: number, length: number, label: string) {
  const prefix = sql.slice(0, index);
  const lineNumber = prefix.split(/\r\n|\r|\n/).length;
  const lastLineBreak = Math.max(prefix.lastIndexOf("\n"), prefix.lastIndexOf("\r"));
  const startColumn = index - lastLineBreak;

  return {
    lineNumber,
    startColumn,
    endColumn: startColumn + length,
    label,
  };
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
