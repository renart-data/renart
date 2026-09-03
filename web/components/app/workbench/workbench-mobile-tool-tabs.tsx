import { useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useRef } from "react";

import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

import { appWorkbenchTools, type AppWorkbenchTool } from "../app-navigation-model";
import { useWorkbench } from "./workbench-slots";

export function AppWorkbenchMobileToolTabs() {
  const {
    navigation,
    session,
    dispatch,
    hasContextSlot,
    hasToolAction,
    invokeToolAction,
    mobileNavigationOpen,
    setMobileNavigationOpen,
  } = useWorkbench();
  const navigate = useNavigate();
  const activeToolRef = useRef<HTMLButtonElement | null>(null);
  const tools = useMemo(
    () =>
      navigation
        ? (appWorkbenchTools[navigation.mode] as readonly AppWorkbenchTool[]).filter(
            (tool) => Boolean(tool.to) || hasToolAction(tool.id),
          )
        : [],
    [hasToolAction, navigation],
  );
  const modeState = navigation ? session.modes[navigation.mode] : null;

  useEffect(() => {
    activeToolRef.current?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [modeState?.activeTool]);

  if (!navigation?.workbench || !modeState || tools.length === 0) return null;

  const activateTool = (tool: AppWorkbenchTool) => {
    const active = tool.id === modeState.activeTool;
    if (active) {
      if (tool.contextual && hasContextSlot) {
        setMobileNavigationOpen(!mobileNavigationOpen);
      } else if (hasToolAction(tool.id)) {
        invokeToolAction(tool.id);
      }
      return;
    }

    setMobileNavigationOpen(false);
    dispatch({ type: "tool-selected", mode: tool.mode, tool: tool.id });
    if (hasToolAction(tool.id)) {
      invokeToolAction(tool.id);
      return;
    }
    if (tool.to) {
      void navigate({ to: tool.to as never }).then(() => {
        if (tool.contextual) {
          window.setTimeout(() => setMobileNavigationOpen(true), 0);
        }
      });
    }
  };

  const toolById = new Map(tools.map((tool) => [tool.id, tool]));

  return (
    <div className="no-scrollbar shrink-0 overflow-x-auto border-b bg-background md:hidden">
      <Tabs
        value={modeState.activeTool}
        onValueChange={(value) => {
          const tool = toolById.get(value as AppWorkbenchTool["id"]);
          if (tool) activateTool(tool);
        }}
        className="block min-w-max gap-0"
      >
        <TabsList
          variant="line"
          aria-label={`${navigation.mode} tools`}
          className="h-10 w-max gap-0 rounded-none p-0"
        >
          {tools.map((tool) => {
            const active = tool.id === modeState.activeTool;
            const Icon = tool.icon;
            return (
              <TabsTrigger
                key={tool.id}
                ref={active ? activeToolRef : undefined}
                value={tool.id}
                className="h-10 flex-none rounded-none px-3 text-[11px] after:bottom-0"
                onClick={() => {
                  if (active) activateTool(tool);
                }}
              >
                <Icon className="size-3.5" />
                <span>{tool.mobileLabel ?? tool.label}</span>
              </TabsTrigger>
            );
          })}
        </TabsList>
      </Tabs>
    </div>
  );
}
