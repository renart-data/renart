export function withSQLPreviewLimit(query: string, limit: number) {
  const normalizedLimit = Math.max(0, Math.floor(limit));
  const statement = query.trim().replace(/;\s*$/, "");
  if (!statement || normalizedLimit === 0) return statement;

  const trailingLimit = /\blimit\s+(\d+)(\s+offset\s+\d+)?\s*$/i;
  const match = trailingLimit.exec(statement);
  if (match) {
    const existingLimit = Number(match[1]);
    if (Number.isFinite(existingLimit) && existingLimit <= normalizedLimit) {
      return statement;
    }
    return `${statement.slice(0, match.index)}LIMIT ${normalizedLimit}${match[2] ?? ""}`;
  }

  return `${statement}\nLIMIT ${normalizedLimit}`;
}
