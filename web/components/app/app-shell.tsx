import { Link, Outlet, useLocation, useMatches, useParams } from "@tanstack/react-router";
import {
  BookOpen,
  Boxes,
  Check,
  ChevronDown,
  ChevronRight,
  FileCode,
  GitBranch,
  GitCommit,
  Loader2,
  Lock,
  RefreshCw,
  Clock,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useAtomValue, useSetAtom } from "jotai";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { useSourceControl } from "@/hooks/use-source-control";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { useWorkspaceSync } from "@/hooks/use-workspace-sync";
import {
  selectedEnvironmentAtom,
  selectedEnvironmentOverrideAtom,
  selectedExecutionTimeWindowAtom,
  workspaceAtom,
} from "@/lib/atoms/domains/workspace";
import { findExecutionTimeOption, getExecutionTimeOptions } from "@/lib/execution-time";
import type { SourceControlChange, WebNotebook } from "@/lib/types";
import { cn } from "@/lib/utils";
import { getPinnedProjectId } from "@/lib/project-context";
import { rememberWorkspaceProject } from "@/lib/project-route-bootstrap";

import { ProjectSwitcher } from "./project-switcher";
import { AppCommandPalette } from "./app-command-palette";
import {
  appNavigationModes,
  modeForAppPath,
  navigationForAppRouteMatches,
} from "./app-navigation-model";
import { LocalVaultControl } from "./local-vault-control";
import { ServerOfflineOverlay } from "./server-offline-overlay";
import { SourceControlDiffViewer } from "./source-control-diff-viewer";
import { AppWorkbenchLayout } from "./workbench/workbench-layout";
import { AppWorkbenchMobileToolTabs } from "./workbench/workbench-mobile-tool-tabs";
import { WorkbenchProvider } from "./workbench/workbench-slots";

// Stage/unstage row actions reveal on hover on pointer devices, but touch
// devices have no hover state — so always show them where hover isn't
// available (and on keyboard focus).
const revealRowActionClass =
  "opacity-100 focus-visible:opacity-100 [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100 [@media(hover:hover)]:group-focus-within:opacity-100";

export function AppShell() {
  useWorkspaceSync();
  useWorkspaceTheme();
  const sourceControl = useSourceControl();
  const location = useLocation();
  const matchedRouteNavigation = useMatches({
    select: (matches) => navigationForAppRouteMatches(matches),
  });
  const routeNavigation = useMemo(() => {
    if (!matchedRouteNavigation || matchedRouteNavigation.mode !== "build") {
      return matchedRouteNavigation;
    }
    const search = location.search as { editor?: unknown };
    if (search.editor !== "adhoc") return matchedRouteNavigation;
    return {
      ...matchedRouteNavigation,
      tool: "ad-hoc" as const,
    };
  }, [location.search, matchedRouteNavigation]);
  const activeMode = routeNavigation
    ? { id: routeNavigation.mode }
    : modeForAppPath(location.pathname);
  const { workspaceConfig } = useWorkspaceSettingsData();
  const projectId = getPinnedProjectId() ?? workspaceConfig?.project_id ?? "default";
  useEffect(() => {
    if (workspaceConfig?.project_id) rememberWorkspaceProject(workspaceConfig.project_id);
  }, [workspaceConfig?.project_id]);

  return (
    <WorkbenchProvider navigation={routeNavigation} projectId={projectId}>
      {/* h-dvh (not h-screen): 100vh is the *largest* mobile viewport, so the
          bottom nav slides out of sight while the browser chrome is visible. */}
      <div
        className="flex h-dvh min-h-0 flex-col bg-muted/40 text-foreground"
        data-app-mode={activeMode?.id}
        data-workbench-route={routeNavigation?.workbench ? "migrated" : "legacy"}
      >
        <ServerOfflineOverlay />
        <AppHeader sourceControl={sourceControl} activeMode={activeMode?.id ?? null} />
        <AppWorkbenchMobileToolTabs />

        <main className="min-h-0 flex-1 overflow-hidden">
          <AppWorkbenchLayout>
            <Outlet />
          </AppWorkbenchLayout>
        </main>

        <nav
          aria-label="Primary navigation"
          className="grid h-[calc(3.5rem+env(safe-area-inset-bottom))] shrink-0 grid-cols-3 border-t bg-background pb-[env(safe-area-inset-bottom)] md:hidden"
        >
          {appNavigationModes.map((mode) => (
            <Link
              key={mode.id}
              to={mode.to}
              aria-current={activeMode?.id === mode.id ? "page" : undefined}
              className={cn(
                "flex flex-col items-center justify-center gap-0.5 text-[10px] text-muted-foreground",
                activeMode?.id === mode.id && "text-primary",
              )}
            >
              <mode.icon className="size-4" />
              {mode.label}
            </Link>
          ))}
        </nav>
      </div>
    </WorkbenchProvider>
  );
}

function AppHeader({
  sourceControl,
  activeMode,
}: {
  sourceControl: ReturnType<typeof useSourceControl>;
  activeMode: "build" | "run" | "explore" | null;
}) {
  return (
    <header className="flex h-12 shrink-0 items-center border-b border-zinc-800 bg-zinc-950 px-2 text-zinc-100 sm:px-3">
      <Link to="/" className="flex items-center gap-2 pr-2 sm:pr-3">
        <img src="/icons/icon.svg" alt="" aria-hidden className="size-7 rounded-lg" />
        <span className="hidden font-semibold tracking-tight sm:inline">renart</span>
      </Link>

      <ProjectSwitcher />

      <nav className="ml-2 hidden items-center md:flex">
        {appNavigationModes.map((mode) => (
          <Button
            key={mode.id}
            asChild
            size="sm"
            variant="ghost"
            className="relative h-12 rounded-none px-3 text-zinc-400 hover:bg-transparent hover:text-zinc-200 data-[state=open]:bg-transparent"
          >
            <Link
              to={mode.to}
              aria-current={activeMode === mode.id ? "page" : undefined}
              className={cn(
                activeMode === mode.id &&
                  "text-white after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-primary",
              )}
            >
              <mode.icon className="size-3.5" />
              <span>{mode.label}</span>
            </Link>
          </Button>
        ))}
      </nav>

      <div className="flex-1" />

      <AppExecutionSelector />
      <LocalVaultControl />

      <Sheet>
        <SheetTrigger asChild>
          <Button
            aria-label="Source control"
            variant="ghost"
            size="sm"
            className="h-8 px-2 text-zinc-300 hover:bg-zinc-800 hover:text-white"
          >
            <GitBranch className="size-4" />
            <span className="hidden max-w-32 truncate sm:inline">
              {sourceControl.repository?.branch || "git"}
            </span>
            {sourceControl.repository && sourceControl.repository.changes.length > 0 ? (
              <span className="rounded bg-zinc-800 px-1 text-[10px] text-zinc-400">
                {sourceControl.repository.changes.length}
              </span>
            ) : null}
          </Button>
        </SheetTrigger>
        <GitSheet sourceControl={sourceControl} />
      </Sheet>
      <AppCommandPalette />
    </header>
  );
}

function AppExecutionSelector() {
  const workspace = useAtomValue(workspaceAtom);
  const location = useLocation();
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const setSelectedEnvironmentOverride = useSetAtom(selectedEnvironmentOverrideAtom);
  const selectedExecutionTimeWindow = useAtomValue(selectedExecutionTimeWindowAtom);
  const setSelectedExecutionTimeWindow = useSetAtom(selectedExecutionTimeWindowAtom);
  const { normalizedConfigEnvironments } = useWorkspaceSettingsData();
  const params = useParams({ strict: false }) as { pipelineId?: string };
  const isBuildPage = location.pathname.startsWith("/pipelines");
  const pipeline = useMemo(
    () =>
      workspace?.pipelines.find((item) => item.id === params.pipelineId) ??
      workspace?.pipelines[0] ??
      null,
    [params.pipelineId, workspace?.pipelines],
  );
  const availableEnvironmentNames = useMemo(
    () =>
      normalizedConfigEnvironments
        .map((environment) => environment.name)
        .filter((name) => name !== "default"),
    [normalizedConfigEnvironments],
  );
  const options = useMemo(
    () => (isBuildPage ? getExecutionTimeOptions(pipeline?.schedule) : []),
    [isBuildPage, pipeline?.schedule],
  );
  const selectedOption = useMemo(
    () => findExecutionTimeOption(options, selectedExecutionTimeWindow?.start),
    [options, selectedExecutionTimeWindow?.start],
  );
  const selectedValue = selectedOption?.value ?? options[0]?.value ?? "";
  const environmentValue = selectedEnvironment || "__default__";
  const environmentLabel = selectedEnvironment || "default";
  const environmentPolicies = workspace?.environment_policies;
  const selectedPolicy = environmentPolicies?.[environmentLabel];

  useEffect(() => {
    if (!isBuildPage) return;
    if (!selectedOption) return;
    if (
      selectedExecutionTimeWindow?.start === selectedOption.start &&
      selectedExecutionTimeWindow?.end === selectedOption.end
    )
      return;
    setSelectedExecutionTimeWindow({ start: selectedOption.start, end: selectedOption.end });
  }, [
    isBuildPage,
    selectedExecutionTimeWindow?.end,
    selectedExecutionTimeWindow?.start,
    selectedOption,
    setSelectedExecutionTimeWindow,
  ]);

  const handleEnvironmentChange = (nextEnvironment: string) => {
    setSelectedEnvironmentOverride(nextEnvironment === "__default__" ? undefined : nextEnvironment);
  };
  const handleExecutionTimeChange = (value: string) => {
    const option = findExecutionTimeOption(options, value);
    setSelectedExecutionTimeWindow(option ? { start: option.start, end: option.end } : null);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          aria-label="Execution context"
          title={
            selectedPolicy?.protected
              ? `Environment ${environmentLabel} is protected: interactive execution is disabled`
              : undefined
          }
          className={`mx-1 flex h-7 min-w-0 items-center gap-1.5 rounded-full border py-0 pl-1 pr-2 text-xs ${selectedPolicy?.protected ? "border-red-700 bg-red-950 text-red-100 hover:bg-red-900 hover:text-white" : "border-zinc-700 bg-zinc-950 text-zinc-200 hover:bg-zinc-800 hover:text-white"}`}
        >
          <span
            className={`flex size-5 shrink-0 items-center justify-center rounded-full text-[10px] font-bold uppercase text-zinc-900 ${selectedPolicy?.protected ? "bg-red-500 text-white" : "bg-amber-500"}`}
          >
            {selectedPolicy?.protected ? <Lock className="size-3" /> : environmentLabel.slice(0, 1)}
          </span>
          <span className="max-w-20 truncate font-mono sm:max-w-24">{environmentLabel}</span>
          {isBuildPage ? (
            <span className="hidden min-w-0 items-center gap-1 text-zinc-400 lg:flex">
              <Clock className="size-3" />{" "}
              <span className="max-w-28 truncate">{selectedOption?.label ?? "Latest"}</span>
            </span>
          ) : null}
          <ChevronDown className="size-3 text-zinc-500" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel>Environment</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={environmentValue} onValueChange={handleEnvironmentChange}>
          <DropdownMenuRadioItem value="__default__">
            <Boxes className="size-4" />
            default
          </DropdownMenuRadioItem>
          {availableEnvironmentNames.map((environmentName) => (
            <DropdownMenuRadioItem key={environmentName} value={environmentName}>
              {environmentPolicies?.[environmentName]?.protected ? (
                <Lock className="size-4 text-red-600" />
              ) : (
                <Boxes className="size-4" />
              )}
              <span
                className={
                  environmentPolicies?.[environmentName]?.protected ? "text-red-600" : undefined
                }
              >
                {environmentName}
              </span>
              {environmentPolicies?.[environmentName]?.protected ? (
                <span className="ml-auto text-[10px] uppercase text-red-600">protected</span>
              ) : null}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        {isBuildPage && options.length > 0 ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Time range</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={selectedValue} onValueChange={handleExecutionTimeChange}>
              {options.map((option) => (
                <DropdownMenuRadioItem key={option.value} value={option.value}>
                  <Clock className="size-4" />
                  {option.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function GitSheet({ sourceControl }: { sourceControl: ReturnType<typeof useSourceControl> }) {
  const {
    repository,
    branches,
    loading,
    busy,
    diffLoading,
    diff,
    error,
    refresh,
    loadDiff,
    loadDiffGroup,
    stage,
    unstage,
    checkout,
    initRepository,
    commit,
  } = sourceControl;
  const [message, setMessage] = useState("");
  const workspace = useAtomValue(workspaceAtom);
  const changes = repository?.changes ?? [];
  const stagedChanges = changes.filter((change) => change.staged);
  const unstagedChanges = changes.filter((change) => !change.staged);
  // A notebook is a folder of files (notebook.yml + cell .sql files); collapse
  // each one into a single entry so its diffs read as one unit.
  const notebookFolders = useMemo(
    () => buildNotebookFolders(changes, workspace?.notebooks ?? []),
    [changes, workspace?.notebooks],
  );
  const changedCount = changes.length;
  const branch = repository?.branch || "unknown";
  const hasRepository = repository?.has_repository ?? true;
  const showDiffPane = hasRepository && (diffLoading || Boolean(diff));

  const submitCommit = async () => {
    await commit(message);
    setMessage("");
  };

  if (!hasRepository) {
    return (
      <SheetContent className="gap-0 p-0 !max-w-none !w-full sm:!w-[28rem]">
        <SheetHeader className="pr-12">
          <SheetTitle className="flex items-center gap-2">
            <GitBranch className="size-4 text-primary" />
            Source control
          </SheetTitle>
          <SheetDescription>Review, stage, and commit local workspace changes.</SheetDescription>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-auto p-4 pt-0 text-sm">
          <div className="space-y-4">
            <div className="flex justify-end">
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={busy || loading}
                onClick={() => void refresh()}
              >
                {loading ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <RefreshCw className="size-3.5" />
                )}
              </Button>
            </div>
            {error ? (
              <div className="rounded-md border border-red-200 bg-red-50 p-2 text-xs text-red-700">
                {error}
              </div>
            ) : null}
            <div className="flex min-h-80 flex-col items-center justify-center gap-4 rounded-lg border border-dashed p-8 text-center">
              <div className="flex size-12 items-center justify-center rounded-full bg-muted">
                <GitBranch className="size-6 text-primary" />
              </div>
              <div className="space-y-1">
                <p className="text-sm font-medium">This project is not a git repository yet.</p>
                <p className="text-xs text-muted-foreground">
                  Initializing creates a .gitignore for local runtime files.
                </p>
              </div>
              <Button disabled={busy || loading} onClick={() => void initRepository()}>
                {busy ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <GitBranch className="size-4" />
                )}
                Initialize repository
              </Button>
            </div>
          </div>
        </div>
      </SheetContent>
    );
  }

  return (
    <SheetContent
      className={cn(
        "gap-0 p-0 !max-w-none",
        showDiffPane ? "!w-[calc(100vw-1rem)] sm:!w-[96vw] xl:!w-[72rem]" : "!w-full sm:!w-[28rem]",
      )}
    >
      <SheetHeader className="pr-12">
        <SheetTitle className="flex items-center gap-2">
          <GitBranch className="size-4 text-primary" />
          Source control
        </SheetTitle>
        <SheetDescription>Review, stage, and commit local workspace changes.</SheetDescription>
      </SheetHeader>
      <div
        className={cn(
          "min-h-0 flex-1 overflow-auto p-4 pt-0 text-sm",
          showDiffPane
            ? "grid gap-4 lg:grid-cols-[minmax(28rem,1fr)_22rem] lg:overflow-hidden"
            : null,
        )}
      >
        {showDiffPane ? (
          <div className="order-2 h-72 min-h-0 min-w-0 overflow-hidden lg:order-1 lg:h-auto lg:min-h-0">
            <SourceControlDiffViewer diff={diff} loading={diffLoading} className="h-full" />
          </div>
        ) : null}
        <div
          className={cn("space-y-4", showDiffPane ? "order-1 lg:order-2 lg:overflow-auto" : null)}
        >
          <div className="flex items-center gap-2">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="outline"
                  className="min-w-0 flex-1 justify-start"
                  disabled={busy || loading}
                >
                  <GitBranch className="size-4" />
                  <span className="truncate">{branch}</span>
                  <ChevronDown className="ml-auto size-3.5" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-64">
                <DropdownMenuLabel>Local branches</DropdownMenuLabel>
                {branches.map((item) => (
                  <DropdownMenuItem
                    key={item}
                    disabled={busy || item === branch}
                    onClick={() => void checkout(item)}
                  >
                    <GitBranch className="size-4" />
                    <span className="flex-1 truncate">{item}</span>
                    {item === branch ? <Check className="size-3.5" /> : null}
                  </DropdownMenuItem>
                ))}
                {branches.length === 0 ? (
                  <DropdownMenuItem disabled>No branches found</DropdownMenuItem>
                ) : null}
              </DropdownMenuContent>
            </DropdownMenu>
            <Button
              variant="ghost"
              size="icon-sm"
              disabled={busy || loading}
              onClick={() => void refresh()}
            >
              {loading ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <RefreshCw className="size-3.5" />
              )}
            </Button>
          </div>
          {error ? (
            <div className="rounded-md border border-red-200 bg-red-50 p-2 text-xs text-red-700">
              {error}
            </div>
          ) : null}
          <div className="space-y-2">
            <textarea
              className="min-h-16 w-full resize-none rounded-lg border bg-background p-2 text-sm outline-none"
              placeholder="Commit message..."
              value={message}
              onChange={(event) => setMessage(event.target.value)}
            />
            <Button
              className="w-full"
              disabled={busy || stagedChanges.length === 0 || !message.trim()}
              onClick={() => void submitCommit()}
            >
              {busy ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <GitCommit className="size-4" />
              )}
              Commit {stagedChanges.length} file{stagedChanges.length === 1 ? "" : "s"}
            </Button>
          </div>
          <ChangeGroup
            title={`Staged · ${stagedChanges.length}`}
            changes={stagedChanges}
            actionLabel="Unstage"
            busy={busy}
            notebookFolders={notebookFolders}
            selectedPath={diff?.staged ? diff.path : ""}
            onSelect={(path) => void loadDiff(path, true)}
            onSelectGroup={(paths, label) => void loadDiffGroup(paths, true, label)}
            onAction={(paths) => void unstage(paths)}
          />
          <ChangeGroup
            title={`Changes · ${unstagedChanges.length}`}
            changes={unstagedChanges}
            actionLabel="Stage"
            busy={busy}
            notebookFolders={notebookFolders}
            selectedPath={!diff?.staged ? diff?.path : ""}
            onSelect={(path) => void loadDiff(path, false)}
            onSelectGroup={(paths, label) => void loadDiffGroup(paths, false, label)}
            onAction={(paths) => void stage(paths)}
          />
          {changedCount > 0 ? (
            <div className="grid grid-cols-2 gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={busy || unstagedChanges.length === 0}
                onClick={() => void stage(unstagedChanges.map((change) => change.path))}
              >
                Stage all
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={busy || stagedChanges.length === 0}
                onClick={() => void unstage(stagedChanges.map((change) => change.path))}
              >
                Unstage all
              </Button>
            </div>
          ) : null}
          {!loading && changedCount === 0 ? (
            <div className="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">
              Working tree clean.
            </div>
          ) : null}
        </div>
      </div>
    </SheetContent>
  );
}

// Map of on-disk notebook folder path → display title. Folders are detected
// from changed `notebook.yml` manifests (authoritative, carries the real
// prefix) and from the loaded workspace notebooks (matched by suffix so it
// works even when the workspace root sits below the git root).
function buildNotebookFolders(
  changes: SourceControlChange[],
  notebooks: WebNotebook[],
): Map<string, string> {
  const folders = new Map<string, string>();
  const manifestSuffix = "/notebook.yml";

  for (const change of changes) {
    if (change.path.endsWith(manifestSuffix)) {
      const folder = change.path.slice(0, -manifestSuffix.length);
      folders.set(folder, folder.split("/").pop() || folder);
    }
  }

  for (const notebook of notebooks) {
    const notebookPath = notebook.path.replace(/\/+$/, "");
    if (!notebookPath) {
      continue;
    }
    if (folders.has(notebookPath)) {
      folders.set(notebookPath, notebook.title);
      continue;
    }
    // Recover the real folder (with any git-root prefix) from a change beneath
    // this notebook, then prefer the human title.
    const needle = `${notebookPath}/`;
    for (const change of changes) {
      const index = change.path.indexOf(needle);
      if (index === 0 || (index > 0 && change.path[index - 1] === "/")) {
        folders.set(change.path.slice(0, index + notebookPath.length), notebook.title);
        break;
      }
    }
  }

  return folders;
}

function notebookFolderForPath(path: string, folders: Map<string, string>): string | null {
  for (const folder of folders.keys()) {
    if (path === folder || path.startsWith(`${folder}/`)) {
      return folder;
    }
  }
  return null;
}

function ChangeGroup({
  title,
  changes,
  actionLabel,
  busy,
  notebookFolders,
  selectedPath,
  onSelect,
  onSelectGroup,
  onAction,
}: {
  title: string;
  changes: SourceControlChange[];
  actionLabel: string;
  busy: boolean;
  notebookFolders: Map<string, string>;
  selectedPath?: string;
  onSelect: (path: string) => void;
  onSelectGroup: (paths: string[], label: string) => void;
  onAction: (paths: string[]) => void;
}) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  if (changes.length === 0) {
    return null;
  }

  // Partition into notebook groups (in first-seen order) and loose files.
  const groupOrder: string[] = [];
  const grouped = new Map<string, SourceControlChange[]>();
  const loose: SourceControlChange[] = [];
  for (const change of changes) {
    const folder = notebookFolderForPath(change.path, notebookFolders);
    if (folder) {
      if (!grouped.has(folder)) {
        grouped.set(folder, []);
        groupOrder.push(folder);
      }
      grouped.get(folder)!.push(change);
    } else {
      loose.push(change);
    }
  }

  const toggle = (folder: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(folder)) {
        next.delete(folder);
      } else {
        next.add(folder);
      }
      return next;
    });
  };

  return (
    <div>
      <div className="mb-2 text-xs font-semibold uppercase text-muted-foreground">{title}</div>
      {groupOrder.map((folder) => {
        const folderChanges = grouped.get(folder)!;
        const paths = folderChanges.map((change) => change.path);
        const isOpen = expanded.has(folder);
        return (
          <div key={`${title}-nb-${folder}`}>
            <div
              className={`group flex min-h-8 items-center gap-1 rounded-md hover:bg-muted ${selectedPath === folder ? "bg-muted" : ""}`}
            >
              <button
                type="button"
                className="flex size-5 shrink-0 items-center justify-center text-muted-foreground"
                onClick={() => toggle(folder)}
                title={isOpen ? "Collapse" : "Expand"}
              >
                {isOpen ? (
                  <ChevronDown className="size-3.5" />
                ) : (
                  <ChevronRight className="size-3.5" />
                )}
              </button>
              <button
                type="button"
                className="flex min-h-8 min-w-0 flex-1 items-center gap-2 truncate rounded-md px-1 text-left"
                onClick={() => onSelectGroup(paths, folder)}
              >
                <BookOpen className="size-3.5 shrink-0 text-primary" />
                <span className="truncate text-xs font-medium">{notebookFolders.get(folder)}</span>
                <span className="shrink-0 text-[11px] text-muted-foreground">
                  {folderChanges.length} file{folderChanges.length === 1 ? "" : "s"}
                </span>
              </button>
              <Button
                variant="ghost"
                size="xs"
                className={revealRowActionClass}
                disabled={busy}
                onClick={() => onAction(paths)}
              >
                {actionLabel} all
              </Button>
            </div>
            {isOpen ? (
              <div className="ml-4 border-l pl-1">
                {folderChanges.map((change) => (
                  <ChangeFileRow
                    key={`${title}-${change.path}`}
                    change={change}
                    actionLabel={actionLabel}
                    busy={busy}
                    selected={selectedPath === change.path}
                    onSelect={onSelect}
                    onAction={onAction}
                    displayPath={notebookRelativePath(change.path, folder)}
                  />
                ))}
              </div>
            ) : null}
          </div>
        );
      })}
      {loose.map((change) => (
        <ChangeFileRow
          key={`${title}-${change.path}`}
          change={change}
          actionLabel={actionLabel}
          busy={busy}
          selected={selectedPath === change.path}
          onSelect={onSelect}
          onAction={onAction}
          displayPath={change.path}
        />
      ))}
    </div>
  );
}

function notebookRelativePath(path: string, folder: string): string {
  return path.startsWith(`${folder}/`) ? path.slice(folder.length + 1) : path;
}

function ChangeFileRow({
  change,
  actionLabel,
  busy,
  selected,
  onSelect,
  onAction,
  displayPath,
}: {
  change: SourceControlChange;
  actionLabel: string;
  busy: boolean;
  selected: boolean;
  onSelect: (path: string) => void;
  onAction: (paths: string[]) => void;
  displayPath: string;
}) {
  return (
    <div
      className={`group flex min-h-8 items-center rounded-md hover:bg-muted ${selected ? "bg-muted" : ""}`}
    >
      <button
        type="button"
        className="flex min-h-8 min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-left"
        onClick={() => onSelect(change.path)}
      >
        <FileCode className="size-3.5 shrink-0 text-primary" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{displayPath}</span>
        <span className="font-mono text-xs text-amber-600">{sourceControlStatusLabel(change)}</span>
      </button>
      <Button
        variant="ghost"
        size="xs"
        className={revealRowActionClass}
        disabled={busy}
        onClick={() => onAction([change.path])}
      >
        {actionLabel}
      </Button>
    </div>
  );
}

function sourceControlStatusLabel(change: SourceControlChange) {
  const staged = change.staged_status.trim();
  const worktree = change.worktree_status.trim();
  return staged || worktree || "M";
}
