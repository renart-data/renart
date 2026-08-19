export const NOTEBOOK_CHART_SERIES_CAP = 20;

export function boundedRowsToObjects(
  columns: string[],
  rows: unknown[][],
  limit: number,
): Record<string, unknown>[] {
  return rows.slice(0, Math.max(0, limit)).map((row) => {
    const object: Record<string, unknown> = {};
    columns.forEach((column, index) => {
      object[column] = row[index];
    });
    return object;
  });
}

export function pivotChartSeries(
  rows: Record<string, unknown>[],
  xKey: string,
  valueKey: string,
  seriesKey: string,
  seriesLimit = NOTEBOOK_CHART_SERIES_CAP,
) {
  const byX = new Map<string, Record<string, unknown>>();
  const series: string[] = [];
  const includedSeries = new Set<string>();
  const allSeries = new Set<string>();
  const boundedSeriesLimit = Math.max(1, seriesLimit);

  for (const row of rows) {
    const seriesValue = String(row[seriesKey] ?? "");
    allSeries.add(seriesValue);
    if (!includedSeries.has(seriesValue)) {
      if (series.length >= boundedSeriesLimit) {
        continue;
      }
      includedSeries.add(seriesValue);
      series.push(seriesValue);
    }
    const x = String(row[xKey] ?? "");
    const target = byX.get(x) ?? { [xKey]: row[xKey] };
    target[seriesValue] = row[valueKey];
    byX.set(x, target);
  }

  return { data: [...byX.values()], series, totalSeries: allSeries.size };
}
