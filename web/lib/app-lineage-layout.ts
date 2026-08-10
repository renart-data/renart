export type AppLineageLayoutId = "strict" | "bands" | "force";

export type AppLineageLayoutNode = {
  id: string;
  layer: string;
  name: string;
  width?: number;
  height?: number;
};

export type AppLineageLayoutEdge = {
  source: string;
  target: string;
  provisional?: boolean;
};

export type AppLineageLayoutRecommendation = {
  id: AppLineageLayoutId;
  reason: string;
  scores: Record<AppLineageLayoutId, number> | null;
};

type Graph = {
  nodes: Required<AppLineageLayoutNode>[];
  edges: Array<AppLineageLayoutEdge & { key: string }>;
};

type Analysis = {
  preds: Map<string, string[]>;
  succs: Map<string, string[]>;
  nodeById: Map<string, Required<AppLineageLayoutNode>>;
  layerOrder: string[];
  layerIndex: Map<string, number>;
  layerLinearizable: boolean;
  backEdges: Graph["edges"];
  skipEdges: Graph["edges"];
  intraEdges: Graph["edges"];
  nodeRank: Map<string, number>;
  cyclicNodes: string[];
};

export type AppLineageLayoutResult = {
  layoutId: AppLineageLayoutId;
  recommendation: AppLineageLayoutRecommendation | null;
  positions: Map<string, { x: number; y: number }>;
  analysis: Pick<
    Analysis,
    "layerOrder" | "layerLinearizable" | "backEdges" | "skipEdges" | "intraEdges" | "cyclicNodes"
  >;
};

const DEFAULT_NODE_WIDTH = 232;
const DEFAULT_NODE_HEIGHT = 96;
const ROW_GAP = 124;
const COL_GAP = 90;
const LEFT_PAD = 40;
const TOP_PAD = 96;
const LARGE_GRAPH_NODE_THRESHOLD = 800;
const LARGE_GRAPH_EDGE_THRESHOLD = 4000;
const INITIAL_VIEWPORT_PADDING = 24;

export function initialCenteredViewportX({
  viewportWidth,
  graphMinX,
  graphMaxX,
  zoom = 1,
  padding = INITIAL_VIEWPORT_PADDING,
}: {
  viewportWidth: number;
  graphMinX: number;
  graphMaxX: number;
  zoom?: number;
  padding?: number;
}) {
  const graphWidth = (graphMaxX - graphMinX) * zoom;
  if (
    !Number.isFinite(graphWidth) ||
    graphWidth <= 0 ||
    !Number.isFinite(viewportWidth) ||
    graphWidth + padding * 2 > viewportWidth
  ) {
    return null;
  }
  return (viewportWidth - graphWidth) / 2 - graphMinX * zoom;
}

type LayeredLayoutItem = {
  id: string;
  realId: string | null;
  rank: number;
  stableIndex: number;
};

type LayeredLayoutEdge = {
  source: string;
  target: string;
};

type CrossingReductionOptions = {
  graphNodeCount: number;
  graphEdgeCount: number;
};

function graphFor(nodes: AppLineageLayoutNode[], edges: AppLineageLayoutEdge[]): Graph {
  const nodeIds = new Set(nodes.map((node) => node.id));
  const edgeSet = new Set<string>();
  return {
    nodes: nodes
      .map((node) => ({
        ...node,
        layer: node.layer || "default",
        name: node.name || node.id,
        width: node.width ?? DEFAULT_NODE_WIDTH,
        height: node.height ?? DEFAULT_NODE_HEIGHT,
      }))
      .sort(
        (a, b) =>
          a.layer.localeCompare(b.layer) ||
          a.name.localeCompare(b.name) ||
          a.id.localeCompare(b.id),
      ),
    edges: edges
      .filter(
        (edge) =>
          edge.source !== edge.target && nodeIds.has(edge.source) && nodeIds.has(edge.target),
      )
      .map((edge) => ({ ...edge, key: `${edge.source}->${edge.target}` }))
      .filter((edge) => {
        if (edgeSet.has(edge.key)) return false;
        edgeSet.add(edge.key);
        return true;
      })
      .sort((a, b) => a.key.localeCompare(b.key)),
  };
}

function buildAdjacency(graph: Graph) {
  const preds = new Map<string, string[]>();
  const succs = new Map<string, string[]>();
  graph.nodes.forEach((node) => {
    preds.set(node.id, []);
    succs.set(node.id, []);
  });
  graph.edges.forEach((edge) => {
    succs.get(edge.source)?.push(edge.target);
    preds.get(edge.target)?.push(edge.source);
  });
  return { preds, succs };
}

function analyze(graph: Graph): Analysis {
  const { preds, succs } = buildAdjacency(graph);
  const nodeById = new Map(graph.nodes.map((node) => [node.id, node]));
  const layersSeen = [...new Set(graph.nodes.map((node) => node.layer))].sort((a, b) =>
    a.localeCompare(b),
  );

  const layerEdgeSet = new Set<string>();
  graph.edges.forEach((edge) => {
    const sourceLayer = nodeById.get(edge.source)?.layer;
    const targetLayer = nodeById.get(edge.target)?.layer;
    if (sourceLayer && targetLayer && sourceLayer !== targetLayer) {
      layerEdgeSet.add(`${sourceLayer}->${targetLayer}`);
    }
  });

  const layerEdges = [...layerEdgeSet].map((key) => key.split("->"));
  const layerIndeg = new Map(layersSeen.map((layer) => [layer, 0]));
  const layerSucc = new Map(layersSeen.map((layer) => [layer, [] as string[]]));
  layerEdges.forEach(([source, target]) => {
    layerIndeg.set(target, (layerIndeg.get(target) ?? 0) + 1);
    layerSucc.get(source)?.push(target);
  });

  const indegWork = new Map(layerIndeg);
  const queue = layersSeen.filter((layer) => indegWork.get(layer) === 0);
  const layerOrder: string[] = [];
  while (queue.length) {
    queue.sort((a, b) => a.localeCompare(b));
    const layer = queue.shift();
    if (!layer) continue;
    layerOrder.push(layer);
    for (const next of layerSucc.get(layer) ?? []) {
      indegWork.set(next, (indegWork.get(next) ?? 0) - 1);
      if (indegWork.get(next) === 0) queue.push(next);
    }
  }
  const layerLinearizable = layerOrder.length === layersSeen.length;
  if (!layerLinearizable) {
    layersSeen.forEach((layer) => {
      if (!layerOrder.includes(layer)) layerOrder.push(layer);
    });
  }
  const layerIndex = new Map(layerOrder.map((layer, index) => [layer, index]));

  const backEdges: Graph["edges"] = [];
  const skipEdges: Graph["edges"] = [];
  const intraEdges: Graph["edges"] = [];
  graph.edges.forEach((edge) => {
    const sourceLayer = nodeById.get(edge.source)?.layer ?? "default";
    const targetLayer = nodeById.get(edge.target)?.layer ?? "default";
    const sourceIndex = layerIndex.get(sourceLayer) ?? 0;
    const targetIndex = layerIndex.get(targetLayer) ?? 0;
    if (targetIndex < sourceIndex) backEdges.push(edge);
    else if (targetIndex === sourceIndex) intraEdges.push(edge);
    else if (targetIndex - sourceIndex > 1) skipEdges.push(edge);
  });

  const indeg = new Map(graph.nodes.map((node) => [node.id, preds.get(node.id)?.length ?? 0]));
  const nodeRank = new Map<string, number>();
  const rankQueue = graph.nodes.filter((node) => indeg.get(node.id) === 0).map((node) => node.id);
  rankQueue.forEach((id) => nodeRank.set(id, 0));
  while (rankQueue.length) {
    const id = rankQueue.shift();
    if (!id) continue;
    for (const target of succs.get(id) ?? []) {
      nodeRank.set(target, Math.max(nodeRank.get(target) ?? 0, (nodeRank.get(id) ?? 0) + 1));
      indeg.set(target, (indeg.get(target) ?? 0) - 1);
      if (indeg.get(target) === 0) rankQueue.push(target);
    }
  }
  const cyclicNodes = graph.nodes.filter((node) => !nodeRank.has(node.id)).map((node) => node.id);
  if (cyclicNodes.length) {
    const fallbackRank = Math.max(0, ...nodeRank.values()) + 1;
    cyclicNodes.forEach((id) => nodeRank.set(id, fallbackRank));
  }

  return {
    preds,
    succs,
    nodeById,
    layerOrder,
    layerIndex,
    layerLinearizable,
    backEdges,
    skipEdges,
    intraEdges,
    nodeRank,
    cyclicNodes,
  };
}

function recommendLayout(graph: Graph, analysis: Analysis): AppLineageLayoutRecommendation | null {
  const nodeCount = graph.nodes.length;
  if (!nodeCount || !graph.edges.length) return null;

  if (analysis.cyclicNodes.length) {
    return {
      id: "force",
      reason:
        "The asset graph contains a cycle, so an organic layout is the safest way to expose the structure while the DAG is fixed.",
      scores: null,
    };
  }

  const edgeCount = graph.edges.length;
  const skipRatio = analysis.skipEdges.length / edgeCount;
  const intraRatio = analysis.intraEdges.length / edgeCount;
  const backRatio = analysis.backEdges.length / edgeCount;
  const layers = analysis.layerOrder.length;
  const density = edgeCount / nodeCount;
  const clean =
    analysis.skipEdges.length + analysis.intraEdges.length + analysis.backEdges.length === 0;

  const scores: Record<AppLineageLayoutId, number> = {
    strict:
      1.0 +
      (layers >= 3 && layers <= 6 ? 0.15 : 0) -
      skipRatio * 1.4 -
      intraRatio * 0.9 -
      backRatio * 5.0,
    bands:
      0.8 +
      (!analysis.layerLinearizable ? 0.6 : 0) +
      intraRatio * 0.5 +
      skipRatio * 0.2 +
      (layers >= 3 ? 0.05 : -0.35) -
      (analysis.layerLinearizable && clean ? 0.2 : 0),
    force: 0.2 + (density > 2.4 ? 0.45 : 0) + (density > 3.4 ? 0.4 : 0),
  };
  const id = Object.entries(scores).sort((a, b) => b[1] - a[1])[0]?.[0] as AppLineageLayoutId;
  const reasons: Record<AppLineageLayoutId, string> = {
    strict: clean
      ? "Layers are linearizable and every edge moves one layer forward, so strict columns match the model."
      : "The flow mostly respects the layer order, so strict columns preserve the prefix model clearly.",
    bands: !analysis.layerLinearizable
      ? "The layer graph is cyclic, but layer bands preserve prefixes while the horizontal axis follows dependency depth."
      : "Intra-layer or skip dependencies make bands more readable than a strict prefix grid.",
    force:
      "The graph is dense enough that cluster structure matters more than rank, so the organic layout is clearer.",
  };
  return { id, reason: reasons[id], scores };
}

function crossingReductionLimits({ graphNodeCount, graphEdgeCount }: CrossingReductionOptions) {
  const large =
    graphNodeCount > LARGE_GRAPH_NODE_THRESHOLD || graphEdgeCount > LARGE_GRAPH_EDGE_THRESHOLD;
  return {
    orderSweeps: large ? 2 : 8,
    transposeSweeps: large ? 1 : 6,
  };
}

function rankPositionMap(ranks: LayeredLayoutItem[][]) {
  const positions = new Map<string, number>();
  ranks.forEach((rank) => rank.forEach((item, index) => positions.set(item.id, index)));
  return positions;
}

function buildLayeredAdjacency(edges: LayeredLayoutEdge[]) {
  const preds = new Map<string, string[]>();
  const succs = new Map<string, string[]>();
  edges.forEach((edge) => {
    succs.set(edge.source, [...(succs.get(edge.source) ?? []), edge.target]);
    preds.set(edge.target, [...(preds.get(edge.target) ?? []), edge.source]);
  });
  return { preds, succs };
}

function countInversions(values: number[]) {
  if (values.length < 2) return 0;
  let inversions = 0;
  let source = values.slice();
  let target = new Array<number>(values.length);
  for (let width = 1; width < values.length; width *= 2) {
    for (let start = 0; start < values.length; start += width * 2) {
      const mid = Math.min(start + width, values.length);
      const end = Math.min(start + width * 2, values.length);
      let left = start;
      let right = mid;
      let out = start;
      while (left < mid && right < end) {
        if (source[left] <= source[right]) {
          target[out] = source[left];
          left += 1;
        } else {
          target[out] = source[right];
          inversions += mid - left;
          right += 1;
        }
        out += 1;
      }
      while (left < mid) {
        target[out] = source[left];
        left += 1;
        out += 1;
      }
      while (right < end) {
        target[out] = source[right];
        right += 1;
        out += 1;
      }
    }
    const swap = source;
    source = target;
    target = swap;
  }
  return inversions;
}

function countBilayerCrossings(
  edges: LayeredLayoutEdge[],
  upperPositions: Map<string, number>,
  lowerPositions: Map<string, number>,
) {
  const orderedTargets = edges
    .map((edge) => {
      const sourcePosition = upperPositions.get(edge.source);
      const targetPosition = lowerPositions.get(edge.target);
      if (sourcePosition === undefined || targetPosition === undefined) return null;
      return { sourcePosition, targetPosition };
    })
    .filter((edge): edge is { sourcePosition: number; targetPosition: number } => edge !== null)
    .sort((a, b) => a.sourcePosition - b.sourcePosition || a.targetPosition - b.targetPosition);
  return countInversions(orderedTargets.map((edge) => edge.targetPosition));
}

function edgesBetweenRanks(edges: LayeredLayoutEdge[], ranks: LayeredLayoutItem[][]) {
  const rankById = new Map<string, number>();
  ranks.forEach((rank, rankIndex) => rank.forEach((item) => rankById.set(item.id, rankIndex)));
  const byRank = Array.from(
    { length: Math.max(0, ranks.length - 1) },
    () => [] as LayeredLayoutEdge[],
  );
  edges.forEach((edge) => {
    const sourceRank = rankById.get(edge.source);
    const targetRank = rankById.get(edge.target);
    if (sourceRank === undefined || targetRank === undefined || targetRank !== sourceRank + 1)
      return;
    byRank[sourceRank]?.push(edge);
  });
  return byRank;
}

function countAdjacentRankCrossings(
  ranks: LayeredLayoutItem[][],
  edgesByRank: LayeredLayoutEdge[][],
  rankIndex: number,
) {
  if (rankIndex < 0 || rankIndex >= edgesByRank.length) return 0;
  const upper = new Map(ranks[rankIndex].map((item, index) => [item.id, index]));
  const lower = new Map(ranks[rankIndex + 1].map((item, index) => [item.id, index]));
  return countBilayerCrossings(edgesByRank[rankIndex], upper, lower);
}

function crossingWindow(
  ranks: LayeredLayoutItem[][],
  edgesByRank: LayeredLayoutEdge[][],
  rankIndex: number,
) {
  return (
    countAdjacentRankCrossings(ranks, edgesByRank, rankIndex - 1) +
    countAdjacentRankCrossings(ranks, edgesByRank, rankIndex)
  );
}

function median(values: number[]) {
  if (!values.length) return null;
  const sorted = values.slice().sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  if (sorted.length % 2) return sorted[middle];
  return (sorted[middle - 1] + sorted[middle]) / 2;
}

function sortLayeredRankByNeighbors(
  rank: LayeredLayoutItem[],
  positions: Map<string, number>,
  neighborsOf: (id: string) => string[],
) {
  const previousPosition = new Map(rank.map((item, index) => [item.id, index]));
  return rank
    .map((item) => {
      const neighborPositions = neighborsOf(item.id)
        .map((neighbor) => positions.get(neighbor))
        .filter((value): value is number => value !== undefined);
      return {
        item,
        score: median(neighborPositions),
        previous: previousPosition.get(item.id) ?? 0,
      };
    })
    .sort((a, b) => {
      if (a.score !== null && b.score !== null && a.score !== b.score) return a.score - b.score;
      if (a.score !== null && b.score === null) return -1;
      if (a.score === null && b.score !== null) return 1;
      return (
        a.previous - b.previous ||
        a.item.stableIndex - b.item.stableIndex ||
        a.item.id.localeCompare(b.item.id)
      );
    })
    .map((entry) => entry.item);
}

function transposeLayeredRanks(
  ranks: LayeredLayoutItem[][],
  edgesByRank: LayeredLayoutEdge[][],
  maxSweeps: number,
) {
  for (let sweep = 0; sweep < maxSweeps; sweep++) {
    let improved = false;
    for (let rankIndex = 0; rankIndex < ranks.length; rankIndex++) {
      const rank = ranks[rankIndex];
      for (let index = 0; index < rank.length - 1; index++) {
        const before = crossingWindow(ranks, edgesByRank, rankIndex);
        const left = rank[index];
        rank[index] = rank[index + 1];
        rank[index + 1] = left;
        const after = crossingWindow(ranks, edgesByRank, rankIndex);
        if (after < before) improved = true;
        else {
          rank[index + 1] = rank[index];
          rank[index] = left;
        }
      }
    }
    if (!improved) break;
  }
}

function minimizeLayeredCrossings(
  ranks: LayeredLayoutItem[][],
  edges: LayeredLayoutEdge[],
  options: CrossingReductionOptions,
) {
  const limits = crossingReductionLimits(options);
  const { preds, succs } = buildLayeredAdjacency(edges);
  const edgesByRank = edgesBetweenRanks(edges, ranks);

  for (let iteration = 0; iteration < limits.orderSweeps; iteration++) {
    let positions = rankPositionMap(ranks);
    for (let rank = 1; rank < ranks.length; rank++) {
      ranks[rank] = sortLayeredRankByNeighbors(ranks[rank], positions, (id) => preds.get(id) ?? []);
      positions = rankPositionMap(ranks);
    }
    positions = rankPositionMap(ranks);
    for (let rank = ranks.length - 2; rank >= 0; rank--) {
      ranks[rank] = sortLayeredRankByNeighbors(ranks[rank], positions, (id) => succs.get(id) ?? []);
      positions = rankPositionMap(ranks);
    }
  }

  transposeLayeredRanks(ranks, edgesByRank, limits.transposeSweeps);
  return ranks;
}

function orderWithinRanks(ranks: string[][], graph: Graph) {
  let stableIndex = 0;
  const layeredRanks: LayeredLayoutItem[][] = ranks.map((rank, rankIndex) =>
    rank.slice().map((id) => ({ id, realId: id, rank: rankIndex, stableIndex: stableIndex++ })),
  );
  const layeredEdges: LayeredLayoutEdge[] = [];
  const rankById = new Map<string, number>();
  layeredRanks.forEach((rank, rankIndex) =>
    rank.forEach((item) => rankById.set(item.id, rankIndex)),
  );

  graph.edges
    .slice()
    .sort((a, b) => a.key.localeCompare(b.key))
    .forEach((edge) => {
      const sourceRank = rankById.get(edge.source);
      const targetRank = rankById.get(edge.target);
      if (sourceRank === undefined || targetRank === undefined || targetRank <= sourceRank) return;
      let previous = edge.source;
      for (let rank = sourceRank + 1; rank < targetRank; rank++) {
        const virtualId = `__virtual:${edge.key}:${rank}`;
        layeredRanks[rank].push({ id: virtualId, realId: null, rank, stableIndex: stableIndex++ });
        layeredEdges.push({ source: previous, target: virtualId });
        previous = virtualId;
      }
      layeredEdges.push({ source: previous, target: edge.target });
    });

  minimizeLayeredCrossings(layeredRanks, layeredEdges, {
    graphNodeCount: graph.nodes.length,
    graphEdgeCount: graph.edges.length,
  });
  return layeredRanks.map((rank) => rank.map((item) => item.id));
}

function orderBandCells(
  cells: Map<string, Required<AppLineageLayoutNode>[]>,
  graph: Graph,
  analysis: Analysis,
  nodeRank: Map<string, number>,
  maxRank: number,
) {
  let stableIndex = 0;
  const rankById = new Map(graph.nodes.map((node) => [node.id, nodeRank.get(node.id) ?? 0]));
  const layerById = new Map(graph.nodes.map((node) => [node.id, node.layer]));
  const virtualItems = new Map<string, LayeredLayoutItem[]>();
  const layeredEdges: LayeredLayoutEdge[] = [];

  graph.edges
    .slice()
    .sort((a, b) => a.key.localeCompare(b.key))
    .forEach((edge) => {
      const sourceRank = rankById.get(edge.source);
      const targetRank = rankById.get(edge.target);
      const sourceLayer = layerById.get(edge.source);
      const targetLayer = layerById.get(edge.target);
      if (sourceRank === undefined || targetRank === undefined || targetRank <= sourceRank) {
        return;
      }

      let previous = edge.source;
      // A long edge inside one band is rendered directly by React Flow. Reserve
      // an empty row in each rank it crosses so a real node cannot be placed on
      // top of that edge. Cross-band skip edges cannot be represented by one
      // unambiguous band-local lane, so they remain out of the row heuristic.
      if (sourceLayer === targetLayer && sourceLayer && targetRank > sourceRank + 1) {
        for (let rank = sourceRank + 1; rank < targetRank; rank++) {
          const virtualId = `__band-virtual:${edge.key}:${rank}`;
          const key = `${sourceLayer}\u0000${rank}`;
          virtualItems.set(key, [
            ...(virtualItems.get(key) ?? []),
            {
              id: virtualId,
              realId: null,
              rank,
              stableIndex: stableIndex++,
            },
          ]);
          layeredEdges.push({ source: previous, target: virtualId });
          previous = virtualId;
        }
      }

      if (targetRank === sourceRank + 1 || previous !== edge.source) {
        layeredEdges.push({ source: previous, target: edge.target });
      }
    });

  const cellItems = new Map<string, LayeredLayoutItem[]>();
  analysis.layerOrder.forEach((layer) => {
    for (let rank = 0; rank <= maxRank; rank++) {
      const key = `${layer}\u0000${rank}`;
      const items = [
        ...(virtualItems.get(key) ?? []),
        ...(cells.get(key) ?? [])
          .slice()
          .sort((a, b) => a.id.localeCompare(b.id))
          .map((node) => ({
            id: node.id,
            realId: node.id,
            rank,
            stableIndex: stableIndex++,
          })),
      ];
      cellItems.set(key, items);
    }
  });

  const limits = crossingReductionLimits({
    graphNodeCount: graph.nodes.length,
    graphEdgeCount: graph.edges.length,
  });
  const { preds, succs } = buildLayeredAdjacency(layeredEdges);

  const allRanks = () => {
    const ranks = Array.from({ length: maxRank + 1 }, () => [] as LayeredLayoutItem[]);
    analysis.layerOrder.forEach((layer) => {
      for (let rank = 0; rank <= maxRank; rank++)
        ranks[rank].push(...(cellItems.get(`${layer}\u0000${rank}`) ?? []));
    });
    return ranks;
  };
  const allEdgesByRank = () => edgesBetweenRanks(layeredEdges, allRanks());
  const allCrossings = (rank: number) => crossingWindow(allRanks(), allEdgesByRank(), rank);
  const positions = () => rankPositionMap(allRanks());

  for (let iteration = 0; iteration < limits.orderSweeps; iteration++) {
    let currentPositions = positions();
    for (let rank = 1; rank <= maxRank; rank++) {
      analysis.layerOrder.forEach((layer) => {
        const key = `${layer}\u0000${rank}`;
        cellItems.set(
          key,
          sortLayeredRankByNeighbors(
            cellItems.get(key) ?? [],
            currentPositions,
            (id) => preds.get(id) ?? [],
          ),
        );
      });
      currentPositions = positions();
    }
    currentPositions = positions();
    for (let rank = maxRank - 1; rank >= 0; rank--) {
      analysis.layerOrder.forEach((layer) => {
        const key = `${layer}\u0000${rank}`;
        cellItems.set(
          key,
          sortLayeredRankByNeighbors(
            cellItems.get(key) ?? [],
            currentPositions,
            (id) => succs.get(id) ?? [],
          ),
        );
      });
      currentPositions = positions();
    }
  }

  for (let sweep = 0; sweep < limits.transposeSweeps; sweep++) {
    let improved = false;
    analysis.layerOrder.forEach((layer) => {
      for (let rank = 0; rank <= maxRank; rank++) {
        const key = `${layer}\u0000${rank}`;
        const items = cellItems.get(key) ?? [];
        for (let index = 0; index < items.length - 1; index++) {
          const before = allCrossings(rank);
          const left = items[index];
          items[index] = items[index + 1];
          items[index + 1] = left;
          const after = allCrossings(rank);
          if (after < before) improved = true;
          else {
            items[index + 1] = items[index];
            items[index] = left;
          }
        }
      }
    });
    if (!improved) break;
  }

  return cellItems;
}

function placeColumns(ranks: string[][], graph: Graph) {
  const positions = new Map<string, { x: number; y: number }>();
  const nodeById = new Map(graph.nodes.map((node) => [node.id, node]));
  const maxRows = Math.max(1, ...ranks.map((rank) => rank.length));
  let x = LEFT_PAD;
  ranks.forEach((rank) => {
    const maxWidth = Math.max(
      DEFAULT_NODE_WIDTH,
      ...rank.map((id) => nodeById.get(id)?.width ?? DEFAULT_NODE_WIDTH),
    );
    const startY = TOP_PAD + ((maxRows - rank.length) * ROW_GAP) / 2;
    rank.forEach((id, index) => {
      const node = nodeById.get(id);
      if (!node) return;
      positions.set(id, { x: x + (maxWidth - node.width) / 2, y: startY + index * ROW_GAP });
    });
    x += maxWidth + COL_GAP;
  });
  return positions;
}

function layoutStrict(graph: Graph, analysis: Analysis) {
  const ranks = analysis.layerOrder.map(() => [] as string[]);
  graph.nodes.forEach((node) => ranks[analysis.layerIndex.get(node.layer) ?? 0].push(node.id));
  return placeColumns(orderWithinRanks(ranks, graph), graph);
}

// A band keeps dependencies inside one prefix readable across multiple
// columns. When the prefix graph itself is acyclic, reserve a complete
// horizontal block for every upstream prefix before placing a downstream
// prefix. This preserves the useful dependency-depth layout within a prefix
// while preventing an unrelated root in a downstream prefix from appearing to
// the left of its prefix's sources.
function bandNodeRanks(graph: Graph, analysis: Analysis) {
  if (!analysis.layerLinearizable) return new Map(analysis.nodeRank);

  const localRanks = new Map<string, number>();
  const layerSpans = new Map<string, number>();
  for (const layer of analysis.layerOrder) {
    const layerNodes = graph.nodes.filter((node) => node.layer === layer);
    const layerIDs = new Set(layerNodes.map((node) => node.id));
    const indegrees = new Map(layerNodes.map((node) => [node.id, 0]));
    const successors = new Map(layerNodes.map((node) => [node.id, [] as string[]]));
    graph.edges.forEach((edge) => {
      if (!layerIDs.has(edge.source) || !layerIDs.has(edge.target)) return;
      successors.get(edge.source)?.push(edge.target);
      indegrees.set(edge.target, (indegrees.get(edge.target) ?? 0) + 1);
    });

    const queue = layerNodes
      .filter((node) => indegrees.get(node.id) === 0)
      .map((node) => node.id)
      .sort((a, b) => a.localeCompare(b));
    queue.forEach((id) => localRanks.set(id, 0));
    let visited = 0;
    while (queue.length) {
      const id = queue.shift();
      if (!id) continue;
      visited += 1;
      for (const target of (successors.get(id) ?? []).sort((a, b) => a.localeCompare(b))) {
        localRanks.set(
          target,
          Math.max(localRanks.get(target) ?? 0, (localRanks.get(id) ?? 0) + 1),
        );
        indegrees.set(target, (indegrees.get(target) ?? 0) - 1);
        if (indegrees.get(target) === 0) {
          queue.push(target);
          queue.sort((a, b) => a.localeCompare(b));
        }
      }
    }
    if (visited !== layerNodes.length) return new Map(analysis.nodeRank);
    layerSpans.set(
      layer,
      Math.max(1, ...layerNodes.map((node) => (localRanks.get(node.id) ?? 0) + 1)),
    );
  }

  const layerBases = new Map(analysis.layerOrder.map((layer) => [layer, 0]));
  for (const layer of analysis.layerOrder) {
    const sourceEnd = (layerBases.get(layer) ?? 0) + (layerSpans.get(layer) ?? 1);
    graph.edges.forEach((edge) => {
      const sourceLayer = analysis.nodeById.get(edge.source)?.layer;
      const targetLayer = analysis.nodeById.get(edge.target)?.layer;
      if (sourceLayer !== layer || !targetLayer || targetLayer === layer) return;
      layerBases.set(targetLayer, Math.max(layerBases.get(targetLayer) ?? 0, sourceEnd));
    });
  }

  return new Map(
    graph.nodes.map((node) => [
      node.id,
      (layerBases.get(node.layer) ?? 0) + (localRanks.get(node.id) ?? 0),
    ]),
  );
}

function layoutBands(graph: Graph, analysis: Analysis) {
  const nodeRank = bandNodeRanks(graph, analysis);
  const maxRank = Math.max(0, ...graph.nodes.map((node) => nodeRank.get(node.id) ?? 0));
  const columnWidths = Array.from({ length: maxRank + 1 }, (_, rank) => {
    const inRank = graph.nodes.filter((node) => nodeRank.get(node.id) === rank);
    return Math.max(DEFAULT_NODE_WIDTH, ...inRank.map((node) => node.width));
  });
  const columnX: number[] = [];
  let x = LEFT_PAD;
  columnWidths.forEach((width) => {
    columnX.push(x);
    x += width + COL_GAP;
  });

  const cells = new Map<string, Required<AppLineageLayoutNode>[]>();
  analysis.layerOrder.forEach((layer) => {
    graph.nodes
      .filter((node) => node.layer === layer)
      .forEach((node) => {
        const rank = nodeRank.get(node.id) ?? 0;
        const key = `${layer}\u0000${rank}`;
        cells.set(key, [...(cells.get(key) ?? []), node]);
      });
  });
  const cellItems = orderBandCells(cells, graph, analysis, nodeRank, maxRank);

  const positions = new Map<string, { x: number; y: number }>();
  let y = 54;
  analysis.layerOrder.forEach((layer) => {
    const inLayer = graph.nodes.filter((node) => node.layer === layer);
    if (!inLayer.length) return;
    const byRank = new Map<number, Required<AppLineageLayoutNode>[]>();
    inLayer.forEach((node) => {
      const rank = nodeRank.get(node.id) ?? 0;
      byRank.set(rank, [...(byRank.get(rank) ?? []), node]);
    });
    const rows = Math.max(
      1,
      ...Array.from(
        { length: maxRank + 1 },
        (_, rank) => cellItems.get(`${layer}\u0000${rank}`)?.length ?? 0,
      ),
    );
    byRank.forEach((nodes, rank) => {
      const orderedIds =
        cellItems.get(`${layer}\u0000${rank}`)?.map((item) => item.id) ??
        nodes.map((node) => node.id);
      const nodeById = new Map(nodes.map((node) => [node.id, node]));
      orderedIds.forEach((id, index) => {
        const node = nodeById.get(id);
        if (!node) return;
        positions.set(node.id, {
          x: columnX[rank] + (columnWidths[rank] - node.width) / 2,
          y: y + 42 + index * ROW_GAP,
        });
      });
    });
    y += 42 + rows * ROW_GAP + 42;
  });
  return positions;
}

function layoutForce(graph: Graph, analysis: Analysis) {
  const centers = graph.nodes.map((node, index) => ({
    id: node.id,
    x:
      LEFT_PAD +
      (analysis.layerIndex.get(node.layer) ?? 0) * 290 +
      (index % 3) * 42 +
      node.width / 2,
    y: TOP_PAD + (index % 7) * ROW_GAP + node.height / 2,
    vx: 0,
    vy: 0,
  }));
  const byId = new Map(centers.map((node) => [node.id, node]));

  for (let tick = 0; tick < 260; tick++) {
    for (let i = 0; i < centers.length; i++) {
      for (let j = i + 1; j < centers.length; j++) {
        const a = centers[i];
        const b = centers[j];
        const dx = b.x - a.x || 0.01;
        const dy = b.y - a.y || 0.01;
        const distSq = Math.max(dx * dx + dy * dy, 100);
        const force = 52000 / distSq;
        const dist = Math.sqrt(distSq);
        const fx = (dx / dist) * force;
        const fy = (dy / dist) * force;
        a.vx -= fx;
        a.vy -= fy;
        b.vx += fx;
        b.vy += fy;
      }
    }
    graph.edges.forEach((edge) => {
      const source = byId.get(edge.source);
      const target = byId.get(edge.target);
      if (!source || !target) return;
      const dx = target.x - source.x || 0.01;
      const dy = target.y - source.y || 0.01;
      const dist = Math.max(Math.sqrt(dx * dx + dy * dy), 1);
      const force = (dist - 280) * 0.015;
      const fx = (dx / dist) * force;
      const fy = (dy / dist) * force;
      source.vx += fx;
      source.vy += fy;
      target.vx -= fx;
      target.vy -= fy;
    });
    centers.forEach((node) => {
      const source = analysis.nodeById.get(node.id);
      const targetX =
        LEFT_PAD +
        (analysis.layerIndex.get(source?.layer ?? "default") ?? 0) * 290 +
        DEFAULT_NODE_WIDTH / 2;
      node.vx += (targetX - node.x) * 0.018;
      node.vy += (TOP_PAD + 220 - node.y) * 0.004;
      node.x += node.vx;
      node.y += node.vy;
      node.vx *= 0.72;
      node.vy *= 0.72;
    });
  }

  const positions = new Map<string, { x: number; y: number }>();
  centers.forEach((center) => {
    const node = analysis.nodeById.get(center.id);
    if (node)
      positions.set(center.id, { x: center.x - node.width / 2, y: center.y - node.height / 2 });
  });
  normalizePositions(positions);
  return positions;
}

function normalizePositions(positions: Map<string, { x: number; y: number }>) {
  if (!positions.size) return;
  const minX = Math.min(...[...positions.values()].map((position) => position.x));
  const minY = Math.min(...[...positions.values()].map((position) => position.y));
  positions.forEach((position, id) => {
    positions.set(id, { x: position.x - minX + LEFT_PAD, y: position.y - minY + TOP_PAD });
  });
}

function computeLayout(layoutId: AppLineageLayoutId, graph: Graph, analysis: Analysis) {
  if (!graph.nodes.length) return new Map<string, { x: number; y: number }>();
  if (layoutId === "bands") return layoutBands(graph, analysis);
  if (layoutId === "force") return layoutForce(graph, analysis);
  return layoutStrict(graph, analysis);
}

export function computeAppLineageLayout({
  nodes,
  edges,
  layoutId,
}: {
  nodes: AppLineageLayoutNode[];
  edges: AppLineageLayoutEdge[];
  layoutId?: AppLineageLayoutId;
}): AppLineageLayoutResult {
  const graph = graphFor(nodes, edges);
  const analysis = analyze(graph);
  const recommendation = recommendLayout(graph, analysis);
  const effectiveLayout = layoutId ?? recommendation?.id ?? "strict";
  return {
    layoutId: effectiveLayout,
    recommendation,
    positions: computeLayout(effectiveLayout, graph, analysis),
    analysis,
  };
}
