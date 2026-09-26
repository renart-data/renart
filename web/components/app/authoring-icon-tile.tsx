import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

const tones = {
  sql: "bg-blue-500/8 text-blue-700 dark:bg-blue-400/12 dark:text-blue-300",
  python: "bg-amber-500/10 text-amber-700 dark:bg-amber-400/12 dark:text-amber-300",
  chart: "bg-violet-500/8 text-violet-700 dark:bg-violet-400/12 dark:text-violet-300",
  control: "bg-teal-500/8 text-teal-700 dark:bg-teal-400/12 dark:text-teal-300",
  text: "bg-muted text-muted-foreground",
};

export function AuthoringIconTile({
  tone,
  compact = false,
  children,
}: {
  tone: keyof typeof tones;
  compact?: boolean;
  children: ReactNode;
}) {
  return (
    <span
      aria-hidden="true"
      data-authoring-icon-tone={tone}
      className={cn(
        "inline-flex shrink-0 items-center justify-center rounded-lg",
        compact ? "h-6 w-9" : "h-10 w-12",
        tones[tone],
      )}
    >
      {children}
    </span>
  );
}
