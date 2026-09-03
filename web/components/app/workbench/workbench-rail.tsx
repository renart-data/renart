import { Link } from "@tanstack/react-router";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

import { appWorkbenchTools, type AppWorkbenchTool } from "../app-navigation-model";
import { useWorkbench } from "./workbench-slots";

export function AppWorkbenchRail() {
  const { navigation, session, hasToolAction } = useWorkbench();
  if (!navigation) return null;

  const tools = (appWorkbenchTools[navigation.mode] as readonly AppWorkbenchTool[]).filter(
    (tool) => Boolean(tool.to) || hasToolAction(tool.id),
  );
  const primary = tools.filter((tool) => tool.position !== "utility");
  const utility = tools.filter((tool) => tool.position === "utility");
  const modeState = session.modes[navigation.mode];

  return (
    <aside
      aria-label={`${navigation.mode} tools`}
      className={cn(
        "flex w-14 shrink-0 flex-col items-center bg-card py-2",
        modeState.sidebarOpen && "border-r",
      )}
    >
      <div className="flex flex-col gap-1">
        {primary.map((tool) => (
          <WorkbenchRailTool key={tool.id} tool={tool} />
        ))}
      </div>
      <div className="mt-auto flex flex-col gap-1">
        {utility.map((tool) => (
          <WorkbenchRailTool key={tool.id} tool={tool} />
        ))}
      </div>
    </aside>
  );
}

function WorkbenchRailTool({ tool }: { tool: AppWorkbenchTool }) {
  const { navigation, session, dispatch, hasToolAction, invokeToolAction } = useWorkbench();
  if (!navigation) return null;
  const modeState = session.modes[tool.mode];
  const active = navigation.mode === tool.mode && modeState.activeTool === tool.id;
  const hasAction = hasToolAction(tool.id);
  const Icon = tool.icon;
  const activate = () => {
    if (active) {
      dispatch({
        type: "active-tool-toggled",
        mode: navigation.mode,
        tool: tool.id,
      });
      return;
    }
    dispatch({ type: "tool-selected", mode: tool.mode, tool: tool.id });
    if (hasAction) invokeToolAction(tool.id);
  };

  const content = (
    <>
      <Icon />
      {active ? <span className="absolute -left-2 h-5 w-0.5 rounded-r bg-primary" /> : null}
    </>
  );

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {hasAction || !tool.to ? (
          <Button
            className={cn(
              "relative size-10",
              active && "bg-primary/10 text-primary hover:bg-primary/15",
            )}
            variant="ghost"
            size="icon-lg"
            aria-label={tool.label}
            aria-pressed={active}
            onClick={activate}
          >
            {content}
          </Button>
        ) : (
          <Button
            asChild
            className={cn(
              "relative size-10",
              active && "bg-primary/10 text-primary hover:bg-primary/15",
            )}
            variant="ghost"
            size="icon-lg"
          >
            <Link
              to={tool.to as never}
              aria-label={tool.label}
              aria-current={active ? "page" : undefined}
              onClick={(event) => {
                if (active) {
                  event.preventDefault();
                  activate();
                  return;
                }
                dispatch({ type: "tool-selected", mode: tool.mode, tool: tool.id });
              }}
            >
              {content}
            </Link>
          </Button>
        )}
      </TooltipTrigger>
      <TooltipContent side="right">{tool.label}</TooltipContent>
    </Tooltip>
  );
}
