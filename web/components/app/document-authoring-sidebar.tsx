"use client";

import type { ReactNode } from "react";

import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

export type DocumentAuthoringTab = {
  value: string;
  label: ReactNode;
  content: ReactNode;
  scroll?: boolean;
};

export function DocumentAuthoringSidebar({
  label,
  tabs,
  value,
  defaultValue,
  className,
  onValueChange,
}: {
  label: string;
  tabs: DocumentAuthoringTab[];
  value?: string;
  defaultValue: string;
  className?: string;
  onValueChange?: (value: string) => void;
}) {
  return (
    <Tabs
      aria-label={label}
      value={value}
      defaultValue={defaultValue}
      className={cn("flex h-full min-h-0 min-w-0 flex-col gap-0", className)}
      onValueChange={onValueChange}
    >
      <div className="shrink-0 border-b p-2">
        <TabsList className="w-full">
          {tabs.map((tab) => (
            <TabsTrigger key={tab.value} value={tab.value} className="min-w-0 flex-1">
              {tab.label}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      {tabs.map((tab) => (
        <TabsContent key={tab.value} value={tab.value} className="min-h-0 min-w-0 flex-1">
          {tab.scroll === false ? (
            <div className="size-full min-h-0 min-w-0">{tab.content}</div>
          ) : (
            <ScrollArea className="h-full">{tab.content}</ScrollArea>
          )}
        </TabsContent>
      ))}
    </Tabs>
  );
}
