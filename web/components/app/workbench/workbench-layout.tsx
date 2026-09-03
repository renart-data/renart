import { type ReactNode } from "react";

import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";

import { AppWorkbenchRail } from "./workbench-rail";
import { useWorkbench } from "./workbench-slots";

export function AppWorkbenchLayout({ children }: { children: ReactNode }) {
  const {
    navigation,
    session,
    hasContextSlot,
    hasInspectorSlot,
    setContextHost,
    setInspectorHost,
    mobileNavigationOpen,
    setMobileNavigationOpen,
  } = useWorkbench();
  const isMobile = useIsMobile();

  if (!navigation?.workbench) return children;

  const modeState = session.modes[navigation.mode];
  const buildEditorSurface = navigation.mode === "build" && navigation.sidebar === "resources";

  return (
    <div className="flex h-full min-h-0 bg-muted/30 md:gap-1.5 md:p-1.5">
      {!isMobile ? (
        <div className="flex shrink-0 overflow-hidden rounded-xl border bg-card shadow-sm">
          <AppWorkbenchRail />
          {modeState.sidebarOpen && hasContextSlot ? (
            <aside
              ref={setContextHost}
              aria-label={`${navigation.mobileLabel} navigation`}
              className="min-h-0 shrink-0 overflow-hidden"
              style={{ width: modeState.sidebarWidth }}
            />
          ) : null}
        </div>
      ) : null}

      <section
        className={cn(
          "min-h-0 min-w-0 flex-1 overflow-hidden",
          buildEditorSurface
            ? "bg-transparent"
            : "bg-background md:rounded-xl md:border md:shadow-sm",
        )}
      >
        {children}
      </section>

      {!isMobile && hasInspectorSlot ? (
        <aside
          ref={setInspectorHost}
          aria-label="Inspector"
          className="hidden min-h-0 w-80 shrink-0 overflow-hidden rounded-xl border bg-card shadow-sm xl:block"
        />
      ) : null}

      {isMobile ? (
        <Sheet open={mobileNavigationOpen} onOpenChange={setMobileNavigationOpen}>
          <SheetContent side="left" className="w-[min(90vw,360px)] gap-0 p-0 sm:max-w-none">
            <SheetHeader className="sr-only">
              <SheetTitle>{navigation.mobileLabel} navigation</SheetTitle>
              <SheetDescription>Browse resources for the selected tool.</SheetDescription>
            </SheetHeader>
            {hasContextSlot ? (
              <div ref={setContextHost} className="min-h-0 flex-1 overflow-hidden" />
            ) : null}
          </SheetContent>
        </Sheet>
      ) : null}
    </div>
  );
}
