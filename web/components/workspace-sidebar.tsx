"use client";

import { Link } from "@tanstack/react-router";
import {
  Cable,
  ChevronRight,
  ChevronsLeft,
  FolderPlus,
  Moon,
  Pencil,
  Play,
  Settings2,
  Sun,
  Trash2,
  Workflow,
} from "lucide-react";
import { CSSProperties, useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from "@/components/ui/sidebar";
import { WebAsset, WorkspaceState } from "@/lib/types";

type Props = {
  workspace: WorkspaceState | null;
  currentView: "workspace" | "environments" | "connections";
  connectionsEnvironment?: string | null;
  activePipeline: string | null;
  selectedAsset: string | null;
  highlighted?: boolean;
  highlightStyle?: CSSProperties;
  theme: "light" | "dark";
  onToggleTheme: () => void;
  onCreatePipeline: () => void;
  onDeletePipeline: (pipelineId: string) => void;
  onRenamePipeline: (pipelineId: string) => void;
  onOpenPipelineSettings: (pipelineId: string) => void;
  canDeletePipeline: boolean;
  deletePipelineLoading: boolean;
  renamePipelineLoading: boolean;
  onRunPipeline: (pipelineId: string) => void;
  canRunPipeline: boolean;
  runPipelineLoading: boolean;
  onOnboardingMountChange?: (element: HTMLDivElement | null) => void;
};

export function WorkspaceSidebar({
  workspace,
  currentView,
  connectionsEnvironment,
  activePipeline,
  selectedAsset,
  highlighted = false,
  highlightStyle,
  theme,
  onToggleTheme,
  onCreatePipeline,
  onDeletePipeline,
  onRenamePipeline,
  onOpenPipelineSettings,
  canDeletePipeline,
  deletePipelineLoading,
  renamePipelineLoading,
  onRunPipeline,
  canRunPipeline,
  runPipelineLoading,
  onOnboardingMountChange,
}: Props) {
  const { isMobile, state, openMobile, setOpenMobile, toggleSidebar } = useSidebar();
  const [expandedPipelineIds, setExpandedPipelineIds] = useState<Set<string>>(
    () => new Set(activePipeline ? [activePipeline] : [])
  );
  const [collapsedAssetGroups, setCollapsedAssetGroups] = useState<Set<string>>(
    () => new Set()
  );
  const pipelineAssetGroups = useMemo(
    () => buildPipelineAssetGroups(workspace),
    [workspace]
  );

  const shouldAutoCloseOnNavigate = isMobile && openMobile;

  const closeSidebarAfterNavigation = () => {
    if (shouldAutoCloseOnNavigate) {
      setOpenMobile(false);
    }
  };

  const handleCreatePipeline = () => {
    closeSidebarAfterNavigation();
    onCreatePipeline();
  };

  useEffect(() => {
    if (!activePipeline) {
      return;
    }

    setExpandedPipelineIds((previous) => {
      if (previous.has(activePipeline)) {
        return previous;
      }

      const next = new Set(previous);
      next.add(activePipeline);
      return next;
    });
  }, [activePipeline]);

  useEffect(() => {
    if (!selectedAsset) {
      return;
    }

    for (const [pipelineId, groups] of Object.entries(pipelineAssetGroups)) {
      for (const group of groups) {
        if (group.assets.some(({ asset }) => asset.id === selectedAsset)) {
          const groupKey = `${pipelineId}:${group.prefix}`;
          setCollapsedAssetGroups((previous) => {
            if (!previous.has(groupKey)) {
              return previous;
            }
            const next = new Set(previous);
            next.delete(groupKey);
            return next;
          });
          return;
        }
      }
    }
  }, [pipelineAssetGroups, selectedAsset]);

  return (
    <Sidebar
      className={`h-full border-r transition-transform md:shadow-none ${
        highlighted ? "ring-2 ring-primary/70 ring-inset" : ""
      }`}
      collapsible="offcanvas"
      style={highlightStyle}
    >
      <SidebarHeader className="border-b px-3 py-3 sm:px-4">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0 flex items-center gap-2 text-sm font-semibold">
            <img
              src="/icons/icon.svg"
              alt="Renart"
              className="size-8 shrink-0 rounded-sm"
            />
            <div className="min-w-0">
              <div className="truncate">Renart</div>
            </div>
          </div>
          <div className="flex items-center gap-1">
            {!isMobile && state === "expanded" ? (
              <Button
                size="icon-sm"
                type="button"
                variant="ghost"
                onClick={toggleSidebar}
                className="shrink-0"
              >
                <ChevronsLeft className="size-3.5" />
              </Button>
            ) : null}
            <Button
              size="icon-sm"
              type="button"
              variant="outline"
              onClick={onToggleTheme}
              className="shrink-0"
            >
              {theme === "dark" ? (
                <Sun className="size-3.5" />
              ) : (
                <Moon className="size-3.5" />
              )}
            </Button>
          </div>
        </div>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Views</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton
                  asChild
                  isActive={currentView === "workspace"}
                >
                  <Link
                    to="/"
                    search={{
                      pipeline: activePipeline ?? undefined,
                      asset: selectedAsset ?? undefined,
                      environment: connectionsEnvironment ?? undefined,
                    }}
                    activeOptions={{ exact: true, includeSearch: false }}
                    onClick={closeSidebarAfterNavigation}
                  >
                    <Workflow className="size-4" />
                    <span>Workspace</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton onClick={handleCreatePipeline}>
                  <FolderPlus className="size-4" />
                  <span>New Pipeline</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton
                  asChild
                  isActive={currentView === "environments"}
                >
                  <Link
                    to={
                      connectionsEnvironment
                        ? "/settings/environments/$environmentId"
                        : "/settings/environments"
                    }
                    params={
                      connectionsEnvironment
                        ? { environmentId: connectionsEnvironment }
                        : undefined
                    }
                    activeOptions={{ exact: true, includeSearch: false }}
                    onClick={closeSidebarAfterNavigation}
                  >
                    <Settings2 className="size-4" />
                    <span>Environments</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton
                  asChild
                  isActive={currentView === "connections"}
                >
                    <Link
                      to={
                        connectionsEnvironment
                          ? "/settings/environments/$environmentId"
                          : "/settings/environments"
                      }
                     params={
                       connectionsEnvironment
                         ? { environmentId: connectionsEnvironment }
                         : undefined
                     }
                    search={{ environment: connectionsEnvironment ?? undefined }}
                    activeOptions={{ exact: true, includeSearch: false }}
                    onClick={closeSidebarAfterNavigation}
                  >
                    <Cable className="size-4" />
                    <span>Connections</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Workspace</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {workspace?.pipelines.map((item) => {
                const isActive = item.id === activePipeline;
                const isExpanded = expandedPipelineIds.has(item.id);
                const assets = item.assets ?? [];

                return (
                  <SidebarMenuItem key={item.id}>
                    <ContextMenu>
                      <ContextMenuTrigger asChild>
                        <div>
                          <SidebarMenuButton asChild isActive={isActive}>
                            <Link
                              to="/"
                              search={{
                                pipeline: item.id,
                                asset: assets[0]?.id ?? undefined,
                                environment: connectionsEnvironment ?? undefined,
                              }}
                              activeOptions={{ exact: true, includeSearch: false }}
                              onClick={closeSidebarAfterNavigation}
                            >
                              <span>{item.name}</span>
                            </Link>
                          </SidebarMenuButton>
                          <SidebarMenuAction
                            aria-label={isExpanded ? "Collapse pipeline" : "Expand pipeline"}
                            onClick={(event) => {
                              event.preventDefault();
                              event.stopPropagation();
                              setExpandedPipelineIds((previous) => {
                                const next = new Set(previous);
                                if (next.has(item.id)) {
                                  next.delete(item.id);
                                } else {
                                  next.add(item.id);
                                }
                                return next;
                              });
                            }}
                            showOnHover
                            type="button"
                          >
                            <ChevronRight
                              className={`size-3 transition-transform ${
                                isExpanded ? "rotate-90" : ""
                              }`}
                            />
                          </SidebarMenuAction>
                        </div>
                      </ContextMenuTrigger>
                      <ContextMenuContent>
                        <ContextMenuItem disabled>{item.name}</ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem
                          onClick={() => onOpenPipelineSettings(item.id)}
                        >
                          <Settings2 />
                          Pipeline Settings
                        </ContextMenuItem>
                        <ContextMenuItem
                          disabled={renamePipelineLoading}
                          onClick={() => onRenamePipeline(item.id)}
                        >
                          <Pencil />
                          Rename Pipeline
                        </ContextMenuItem>
                        <ContextMenuItem
                          disabled={!canRunPipeline || runPipelineLoading}
                          onClick={() => onRunPipeline(item.id)}
                        >
                          <Play
                            className={
                              runPipelineLoading && activePipeline === item.id
                                ? "animate-pulse"
                                : ""
                            }
                          />
                          Run Pipeline
                        </ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem
                          variant="destructive"
                          disabled={!canDeletePipeline || deletePipelineLoading}
                          onClick={() => onDeletePipeline(item.id)}
                        >
                          <Trash2 />
                          Delete Pipeline
                        </ContextMenuItem>
                      </ContextMenuContent>
                    </ContextMenu>

                    {isExpanded && (
                      <SidebarMenuSub>
                        {(pipelineAssetGroups[item.id] ?? []).map((group) => {
                          const groupKey = `${item.id}:${group.prefix}`;
                          const isGroupExpanded = !collapsedAssetGroups.has(groupKey);
                          return (
                            <SidebarMenuSubItem key={group.prefix}>
                              <SidebarMenuSubButton
                                asChild
                                isActive={group.assets.some(({ asset }) => asset.id === selectedAsset)}
                                size="sm"
                              >
                                <button
                                  type="button"
                                  onClick={() => {
                                    setCollapsedAssetGroups((previous) => {
                                      const next = new Set(previous);
                                      if (next.has(groupKey)) {
                                        next.delete(groupKey);
                                      } else {
                                        next.add(groupKey);
                                      }
                                      return next;
                                    });
                                  }}
                                >
                                  <ChevronRight
                                    className={`size-3 transition-transform ${
                                      isGroupExpanded ? "rotate-90" : ""
                                    }`}
                                  />
                                  <span>{group.prefix}</span>
                                </button>
                              </SidebarMenuSubButton>
                              {isGroupExpanded && (
                                <SidebarMenuSub className="ml-3">
                                  {group.assets.map(({ asset, leaf }) => (
                                    <SidebarMenuSubItem key={asset.id}>
                                      <SidebarMenuSubButton
                                        asChild
                                        isActive={asset.id === selectedAsset}
                                        size="sm"
                                      >
                                        <Link
                                          to="/"
                                          search={{
                                            pipeline: item.id,
                                            asset: asset.id,
                                            environment: connectionsEnvironment ?? undefined,
                                          }}
                                          activeOptions={{
                                            exact: true,
                                            includeSearch: false,
                                          }}
                                          onClick={closeSidebarAfterNavigation}
                                        >
                                          <span>{leaf}</span>
                                        </Link>
                                      </SidebarMenuSubButton>
                                    </SidebarMenuSubItem>
                                  ))}
                                </SidebarMenuSub>
                              )}
                            </SidebarMenuSubItem>
                          );
                        })}
                      </SidebarMenuSub>
                    )}
                  </SidebarMenuItem>
                );
              })}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        {onOnboardingMountChange && (
          <SidebarGroup>
            <SidebarGroupLabel>Onboarding</SidebarGroupLabel>
            <SidebarGroupContent>
              <div ref={onOnboardingMountChange} />
            </SidebarGroupContent>
          </SidebarGroup>
        )}
      </SidebarContent>
    </Sidebar>
  );
}

type AssetGroup = {
  prefix: string;
  assets: Array<{ asset: WebAsset; leaf: string }>;
};

function buildPipelineAssetGroups(workspace: WorkspaceState | null): Record<string, AssetGroup[]> {
  const grouped: Record<string, AssetGroup[]> = {};
  for (const pipeline of workspace?.pipelines ?? []) {
    const groupsByPrefix = new Map<string, AssetGroup>();
    for (const asset of pipeline.assets ?? []) {
      const { prefix, leaf } = splitAssetName(asset.name);
      const group = groupsByPrefix.get(prefix) ?? { prefix, assets: [] };
      group.assets.push({ asset, leaf });
      groupsByPrefix.set(prefix, group);
    }
    grouped[pipeline.id] = Array.from(groupsByPrefix.values()).sort((left, right) =>
      left.prefix.localeCompare(right.prefix)
    );
  }
  return grouped;
}

function splitAssetName(name: string): { prefix: string; leaf: string } {
  const parts = name.split(".").filter(Boolean);
  if (parts.length <= 1) {
    return { prefix: "unprefixed", leaf: name };
  }
  return {
    prefix: parts.slice(0, -1).join("."),
    leaf: parts[parts.length - 1],
  };
}
