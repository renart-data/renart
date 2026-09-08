import type {
  DataBrowserChildrenResponse,
  DataBrowserConnection,
  DataBrowserNode,
} from "./generated/api-types";

export type BrowserSearchRequest = {
  connectionId: string;
  parentId?: string;
  prefix?: string;
  namePrefix?: string;
};
export type BrowserSearchBase = {
  connection: DataBrowserConnection;
  parts: string[];
  nodes: DataBrowserNode[];
  truncated?: boolean;
};
export type BrowserCompletion = { value: string; label: string };
export type BrowserSearchPlan = {
  connection?: DataBrowserConnection;
  connections: DataBrowserConnection[];
  nodes: DataBrowserNode[];
  completions: BrowserCompletion[];
  prefix: string;
  back: string;
  label: string;
  truncated?: boolean;
  request?: BrowserSearchRequest;
  error?: string;
};

export const searchRequestKey = (request: BrowserSearchRequest) => JSON.stringify(request);
export const quoteBrowserSegment = (name: string) =>
  /^[\p{L}\p{N}_-]+$/u.test(name) ? name : `"${name.replaceAll('"', '""')}"`;
export const connectionSearchPrefix = (connection: DataBrowserConnection) =>
  `${quoteBrowserSegment(connection.name)}.${connection.source_kind === "warehouse" ? "" : "/"}`;

// Prefix matches first, then substrings and abbreviations such as duckb-def.
// Fuzzy matches only suggest: completed path segments always resolve exactly.
function matchRank(name: string, query: string) {
  name = name.toLowerCase();
  query = query.toLowerCase();
  if (name.startsWith(query)) return 0;
  if (name.includes(query)) return 1;
  let index = 0;
  for (const character of name) if (character === query[index]) index++;
  return index === query.length ? 2 : 3;
}

function matches<T>(items: T[], text: string, label: (item: T) => string) {
  return items
    .map((item, index) => ({ item, index, rank: matchRank(label(item), text) }))
    .filter(({ rank }) => rank < 3)
    .sort((a, b) => a.rank - b.rank || a.index - b.index)
    .map(({ item }) => item);
}

function dottedParts(value: string): string[] | undefined {
  const parts: string[] = [];
  let text = "",
    quoted = false,
    closed = false;
  for (let index = 0; index < value.length; index++) {
    const char = value[index];
    if (char === '"') {
      if (quoted && value[index + 1] === '"') {
        text += '"';
        index++;
      } else if (quoted) {
        quoted = false;
        closed = true;
      } else if (text === "" && !closed) quoted = true;
      else return undefined;
    } else if (char === "." && !quoted) {
      parts.push(text);
      text = "";
      closed = false;
    } else if (closed) return undefined;
    else text += char;
  }
  if (quoted) return undefined;
  return [...parts, text];
}

function completeNodes(plan: BrowserSearchPlan, query: string, filter: string, separator: string) {
  plan.nodes = matches(plan.nodes, filter, (node) => node.label);
  if (!filter) return plan;
  plan.completions = plan.nodes
    .map((node) => ({
      label: node.label,
      value:
        plan.prefix +
        (separator === "." ? quoteBrowserSegment(node.label) : node.label) +
        (node.node_type === "namespace" ? separator : ""),
    }))
    .filter((completion) => completion.value !== query);
  return plan;
}

/** Pure, incremental planner: requests at most one missing level, never siblings
 * or descendants unrelated to the typed path. It never decodes operation IDs. */
export function planDataBrowserSearch(
  query: string,
  connections: DataBrowserConnection[],
  base: BrowserSearchBase | undefined,
  cache: ReadonlyMap<string, DataBrowserChildrenResponse>,
): BrowserSearchPlan {
  const plan: BrowserSearchPlan = {
    connections: [],
    nodes: [],
    completions: [],
    prefix: "",
    back: "",
    label: "Data Browser",
  };
  if (query.length > 4096) return { ...plan, error: "This path is too long." };
  // Longest exact connection wins, including literal dots in configured names.
  const explicitMatches = connections
    .flatMap((connection) =>
      [...new Set([quoteBrowserSegment(connection.name), connection.name])]
        .filter(
          (name) => query.slice(0, name.length + 1).toLowerCase() === `${name}.`.toLowerCase(),
        )
        .map((name) => ({
          connection,
          length: name.length + 1,
          exact: query.startsWith(`${name}.`),
        })),
    )
    .sort((a, b) => b.length - a.length || Number(b.exact) - Number(a.exact));
  const explicit = explicitMatches[0];
  if (
    explicit &&
    explicitMatches.some(
      (item) =>
        item.connection.id !== explicit.connection.id &&
        item.length === explicit.length &&
        item.exact === explicit.exact,
    )
  )
    return { ...plan, error: "This connection name is ambiguous. Use its exact spelling." };
  const connection = explicit?.connection ?? base?.connection;
  if (!connection) {
    plan.connections = matches(connections, query, (item) => item.name);
    if (query)
      plan.completions = plan.connections.map((item) => ({
        label: item.name,
        value: connectionSearchPrefix(item),
      }));
    plan.connections.push(
      ...connections.filter(
        (item) =>
          !plan.connections.includes(item) && item.type.toLowerCase().includes(query.toLowerCase()),
      ),
    );
    return plan;
  }
  plan.connection = connection;
  plan.label = connection.name;
  const root = connectionSearchPrefix(connection);
  let remaining = explicit ? query.slice(explicit.length) : query;
  const initialParts = explicit ? [] : (base?.parts ?? []);
  plan.prefix =
    root +
    initialParts
      .map((part) => (connection.source_kind === "warehouse" ? quoteBrowserSegment(part) : part))
      .join(connection.source_kind === "warehouse" ? "." : "/") +
    (initialParts.length ? (connection.source_kind === "warehouse" ? "." : "/") : "");
  const initialPrefix = plan.prefix;
  const localCompletions = (result: BrowserSearchPlan) => {
    if (!explicit && base)
      result.completions = result.completions
        .map((item) => ({ ...item, value: item.value.slice(initialPrefix.length) }))
        .filter((item) => item.value !== query);
    return result;
  };
  const listing = (request: BrowserSearchRequest) => {
    const cached = cache.get(searchRequestKey(request));
    if (!cached) {
      plan.request = request;
      plan.nodes = [];
      plan.truncated = undefined;
    } else {
      plan.nodes = cached.nodes;
      plan.truncated = cached.truncated;
    }
    return cached;
  };
  if (connection.source_kind === "storage") {
    remaining = remaining.replace(/^\//, "");
    const path = [...initialParts, remaining].join("/");
    if (
      /[\\*?[\]{}|:\p{Cc}]/u.test(path) ||
      path
        .split("/")
        .some(
          (part, index, all) => part === "." || part === ".." || (!part && index < all.length - 1),
        )
    )
      return {
        ...plan,
        error: "Use a path below this connection's root, without wildcards or parent traversal.",
      };
    const slash = path.lastIndexOf("/");
    const parent = path.slice(0, slash + 1),
      filter = path.slice(slash + 1);
    plan.prefix = root + parent;
    plan.back = parent ? root + parent.slice(0, parent.slice(0, -1).lastIndexOf("/") + 1) : "";
    plan.label = parent ? parent.slice(0, -1).split("/").at(-1)! : connection.name;
    const request = { connectionId: connection.id, prefix: parent };
    // A complete parent (or complete literal-prefix subset) can answer further
    // edits locally. S3 prefixes are case-sensitive, unlike local fuzzy matching.
    const candidates: {
      namePrefix: string;
      result: Pick<DataBrowserChildrenResponse, "nodes" | "truncated">;
    }[] = [];
    if (!explicit && base && parent === (initialParts.length ? initialParts.join("/") + "/" : ""))
      candidates.push({ namePrefix: "", result: base });
    for (const [key, result] of cache) {
      const cached = JSON.parse(key) as BrowserSearchRequest;
      if (
        cached.connectionId === connection.id &&
        cached.prefix === parent &&
        filter.startsWith(cached.namePrefix ?? "")
      )
        candidates.push({ namePrefix: cached.namePrefix ?? "", result });
    }
    candidates.sort(
      (a, b) =>
        Number(Boolean(a.result.truncated)) - Number(Boolean(b.result.truncated)) ||
        a.namePrefix.length - b.namePrefix.length,
    );
    const complete = candidates.find((candidate) => !candidate.result.truncated);
    const available =
      complete ?? candidates.find((candidate) => candidate.namePrefix === filter) ?? candidates[0];
    if (!available) {
      listing(request);
      return plan;
    }
    plan.nodes = available.result.nodes;
    plan.truncated = available.result.truncated;
    // An exact prefix means "under this prefix", even without a final slash.
    if (
      filter &&
      plan.nodes.some((node) => node.label === filter && node.node_type === "namespace")
    ) {
      plan.prefix = root + path + "/";
      plan.back = root + parent;
      plan.label = filter;
      plan.completions = [{ label: filter, value: plan.prefix }];
      listing({ connectionId: connection.id, prefix: path + "/" });
      return localCompletions(plan);
    }
    if (connection.type === "s3" && filter && !complete && available.namePrefix !== filter) {
      listing({ ...request, namePrefix: filter });
      return plan;
    }
    // A provider-filtered subset cannot offer complete fuzzy/substring results.
    if (available.namePrefix)
      plan.nodes = plan.nodes.filter((node) => node.label.startsWith(filter));
    return localCompletions(completeNodes(plan, query, filter, "/"));
  }

  const separator = connection.source_kind === "warehouse" ? "." : "/";
  if (separator === "/") remaining = remaining.replace(/^\//, "");
  const parts = separator === "." ? dottedParts(remaining) : remaining.split("/");
  if (!parts) return { ...plan, error: "Finish the quoted name to browse this path." };
  if (parts.length > 32) return { ...plan, error: "This path has too many levels." };
  let nodes: DataBrowserNode[];
  if (!explicit && base) {
    nodes = base.nodes;
    plan.truncated = base.truncated;
  } else {
    const result = listing({ connectionId: connection.id });
    if (!result) return plan;
    nodes = result.nodes;
  }
  let previous = "";
  for (const part of parts.slice(0, -1)) {
    const exact = nodes.filter((node) => node.label === part);
    const candidates = exact.length
      ? exact
      : nodes.filter((node) => node.label.toLowerCase() === part.toLowerCase());
    if (candidates.length !== 1)
      return {
        ...plan,
        nodes: [],
        error: candidates.length
          ? `The name “${part}” is ambiguous. Use its exact spelling.`
          : `“${part}” was not found in this level.${plan.truncated ? " This listing is limited to 500 entries." : ""}`,
      };
    const node = candidates[0];
    if (node.node_type !== "namespace")
      return {
        ...plan,
        nodes: [],
        error: `“${part}” is not a namespace. Open the object to see its columns.`,
      };
    previous = plan.prefix;
    plan.prefix += (separator === "." ? quoteBrowserSegment(node.label) : node.label) + separator;
    plan.label = node.label;
    const result = listing({ connectionId: connection.id, parentId: node.id });
    if (!result) return plan;
    nodes = result.nodes;
  }
  plan.nodes = nodes;
  plan.back = previous;
  return localCompletions(completeNodes(plan, query, parts.at(-1)!, separator));
}
