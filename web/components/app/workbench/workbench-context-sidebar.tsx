import { ArrowLeft } from "lucide-react";
import { useEffect, useRef, type ReactNode, type UIEvent } from "react";

import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";

import { useWorkbench } from "./workbench-slots";

export type AppContextSidebarTransitionDirection = "forward" | "back" | "replace";

export function AppContextSidebarTransition({
  viewKey,
  direction = "replace",
  className,
  children,
}: {
  viewKey: string;
  direction?: AppContextSidebarTransitionDirection;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      key={viewKey}
      data-slot="workbench-context-transition"
      data-transition-key={viewKey}
      data-direction={direction}
      className={cn(
        "flex h-full min-h-0 flex-col animate-in fade-in-0 ease-out motion-reduce:animate-none",
        direction === "forward" && "slide-in-from-right-2 duration-200",
        direction === "back" && "slide-in-from-left-2 duration-200",
        direction === "replace" && "duration-150",
        className,
      )}
    >
      {children}
    </div>
  );
}

export function AppContextSidebarFrame({
  title,
  subtitle,
  actions,
  backLabel,
  onBack,
  transitionKey,
  transitionDirection = "replace",
  children,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  backLabel?: string;
  onBack?: () => void;
  transitionKey?: string;
  transitionDirection?: AppContextSidebarTransitionDirection;
  children: ReactNode;
}) {
  const { navigation, session, dispatch } = useWorkbench();
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const frameRef = useRef<number | null>(null);
  const mode = navigation?.mode;
  const restoredScrollTop = mode ? session.modes[mode].sidebarScrollTop : 0;

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || viewport.scrollTop === restoredScrollTop) return;
    viewport.scrollTop = restoredScrollTop;
  }, [restoredScrollTop, transitionKey, title]);

  useEffect(
    () => () => {
      if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
    },
    [],
  );

  const handleScroll = (event: UIEvent<HTMLDivElement>) => {
    if (!mode || frameRef.current !== null) return;
    const scrollTop = event.currentTarget.scrollTop;
    frameRef.current = requestAnimationFrame(() => {
      frameRef.current = null;
      dispatch({ type: "sidebar-scrolled", mode, scrollTop });
    });
  };

  return (
    <AppContextSidebarTransition
      viewKey={transitionKey ?? title}
      direction={transitionDirection}
      className="bg-card"
    >
      <div
        data-slot="workbench-context-header"
        className="flex h-10 shrink-0 items-center gap-2 border-b px-3 pr-12 md:pr-3"
      >
        {onBack ? (
          <Button variant="ghost" size="icon-sm" aria-label={backLabel ?? "Back"} onClick={onBack}>
            <ArrowLeft />
          </Button>
        ) : null}
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-xs font-semibold">{title}</h2>
          {subtitle ? (
            <p className="truncate text-[10px] text-muted-foreground">{subtitle}</p>
          ) : null}
        </div>
        {actions}
      </div>
      <ScrollArea
        className="min-h-0 flex-1"
        showHorizontalScrollBar={false}
        viewportRef={viewportRef}
        onViewportScroll={handleScroll}
      >
        {children}
      </ScrollArea>
    </AppContextSidebarTransition>
  );
}
