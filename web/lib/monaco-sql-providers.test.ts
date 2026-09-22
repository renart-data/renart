import { describe, expect, it } from "vitest";

import {
  isInsideSingleQuotedSQLString,
  isSQLCompletionSuppressed,
} from "@/lib/monaco-sql-providers";

describe("SQL completion quote detection", () => {
  it("does not let local notebook fallback reintroduce completions in comments or dollar strings", () => {
    for (const sql of [
      "select 1 -- co",
      "select /* co",
      "select /* outer /* inner */ co",
      "select $body$co",
      "select 'co",
    ]) {
      expect(isSQLCompletionSuppressed(sql, sql.length)).toBe(true);
    }
    for (const sql of [
      "select /* done */ co",
      "select $body$done$body$, co",
      "select 1 -- done\nco",
      'select "co',
    ]) {
      expect(isSQLCompletionSuppressed(sql, sql.length)).toBe(false);
    }
  });
  it("suppresses column completion only while the cursor is inside a single-quoted string", () => {
    for (const sql of ["select 'ord from orders", "select 'Ada''s ord from orders"]) {
      expect(isInsideSingleQuotedSQLString(sql, sql.indexOf(" from orders"))).toBe(true);
    }

    const closed = "select 'Ada' from orders";
    expect(isInsideSingleQuotedSQLString(closed, closed.indexOf(" from orders"))).toBe(false);

    const identifier = 'select "ord" from orders';
    expect(isInsideSingleQuotedSQLString(identifier, identifier.indexOf(" from orders"))).toBe(
      false,
    );
  });

  it("ignores quote characters inside comments", () => {
    const lineComment = "select 1 -- 'comment\nfrom orders";
    expect(isInsideSingleQuotedSQLString(lineComment, lineComment.length)).toBe(false);

    const blockComment = "select /* 'comment */ 1 from orders";
    expect(isInsideSingleQuotedSQLString(blockComment, blockComment.length)).toBe(false);
  });
});
