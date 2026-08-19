import type { ReactNode } from "react";

import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";

export function DocumentAuthoringShell({
  commandBar,
  banner,
  children,
  className,
}: {
  commandBar: ReactNode;
  banner?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex h-full min-h-0 flex-col", className)}>
      {commandBar}
      {banner ? <div className="shrink-0 px-3 pt-3">{banner}</div> : null}
      <div className="min-h-0 flex-1">{children}</div>
    </div>
  );
}

export function DocumentAuthoringCommandBar({
  navigation,
  identity,
  mode,
  status,
  history,
  tools,
  actions,
}: {
  navigation: ReactNode;
  identity: ReactNode;
  mode: ReactNode;
  status?: ReactNode;
  history?: ReactNode;
  tools?: ReactNode;
  actions: ReactNode;
}) {
  return (
    <div className="shrink-0 border-b bg-background">
      <div className="flex min-h-12 min-w-0 items-center gap-1.5 px-2">
        <div className="flex shrink-0 items-center gap-1">{navigation}</div>
        <Separator orientation="vertical" className="mx-0.5 hidden h-5 sm:block" />
        <div className="min-w-20 flex-1 sm:max-w-sm">{identity}</div>
        <div className="hidden shrink-0 md:block">{mode}</div>
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {status}
          {history}
          {tools}
          {actions}
        </div>
      </div>
      <div className="flex items-center justify-center border-t px-2 py-1 md:hidden">{mode}</div>
    </div>
  );
}
