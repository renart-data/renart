import { ArrowRight, FolderOpen, FolderGit2, LoaderCircle } from "lucide-react";
import type { ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { ProjectInfo } from "@/lib/generated/api-types";

import { relativeTime, shortPath, type WelcomePath } from "./welcome-flow";

export function WelcomeChoose({
  recent,
  openingProjectId,
  demoMeta,
  onOpenProject,
  onOpenFolder,
  onChoose,
  onHighlight,
}: {
  recent: ProjectInfo[];
  openingProjectId: string | null;
  // What the demo path promises, e.g. "Local data · ready in seconds".
  demoMeta: string;
  onOpenProject: (project: ProjectInfo) => void;
  onOpenFolder: () => void;
  onChoose: (path: WelcomePath) => void;
  // The path card under the pointer or focus, for the preview pane.
  onHighlight: (path: WelcomePath) => void;
}) {
  return (
    <>
      {recent.length > 0 ? (
        <section aria-labelledby="welcome-recent" className="grid gap-2">
          <div className="flex items-center justify-between gap-2">
            <h2 id="welcome-recent" className="text-sm font-medium">
              Recent projects
            </h2>
            <Button variant="ghost" size="sm" onClick={onOpenFolder}>
              <FolderOpen data-icon="inline-start" />
              Open folder...
            </Button>
          </div>
          <ul className="grid overflow-hidden rounded-xl border bg-background">
            {recent.slice(0, 5).map((project) => (
              <li key={project.id} className="border-b last:border-b-0">
                <button
                  type="button"
                  disabled={openingProjectId !== null}
                  onClick={() => onOpenProject(project)}
                  className="group flex w-full min-w-0 items-center gap-3 px-4 py-2.5 text-left transition-colors outline-none hover:bg-muted/50 focus-visible:bg-muted/50 disabled:opacity-60"
                >
                  {openingProjectId === project.id ? (
                    <LoaderCircle className="size-4 shrink-0 animate-spin text-muted-foreground" />
                  ) : (
                    <FolderGit2 className="size-4 shrink-0 text-muted-foreground" />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{project.name}</span>
                    <span
                      className="block truncate font-mono text-[11px] text-muted-foreground"
                      title={project.path}
                    >
                      {shortPath(project.path, 3)}
                    </span>
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {relativeTime(project.last_opened_at)}
                  </span>
                  <ArrowRight className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100" />
                </button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <section aria-labelledby="welcome-start" className="grid gap-2">
        <div className="flex items-center justify-between gap-2">
          <h2 id="welcome-start" className="text-sm font-medium">
            {recent.length > 0 ? "Or start something new" : "How would you like to start?"}
          </h2>
          {recent.length === 0 ? (
            <Button variant="ghost" size="sm" onClick={onOpenFolder}>
              <FolderOpen data-icon="inline-start" />
              Open folder...
            </Button>
          ) : null}
        </div>
        <div className="grid gap-2.5">
          <PathCard
            title="Explore a demo"
            badge={<Badge size="xs">Recommended</Badge>}
            description="A working pipeline on local DuckDB with sample data. Renart builds it once, so you start with real rows to inspect."
            meta={demoMeta}
            illustration={<DemoIllustration />}
            onClick={() => onChoose("demo")}
            onHighlight={() => onHighlight("demo")}
          />
          <PathCard
            title="Connect your data"
            description="A database or warehouse you already use. Pick tables and start a pipeline on top of them."
            meta="Postgres, Snowflake, BigQuery and more"
            illustration={<ConnectIllustration />}
            onClick={() => onChoose("import")}
            onHighlight={() => onHighlight("import")}
          />
          <PathCard
            title="Start from scratch"
            description="An empty pipeline with one example SQL asset on local DuckDB."
            meta="Local data"
            illustration={<ScratchIllustration />}
            onClick={() => onChoose("empty")}
            onHighlight={() => onHighlight("empty")}
          />
        </div>
      </section>
    </>
  );
}

function PathCard({
  title,
  badge,
  description,
  meta,
  illustration,
  onClick,
  onHighlight,
}: {
  title: string;
  badge?: ReactNode;
  description: string;
  meta: string;
  illustration: ReactNode;
  onClick: () => void;
  onHighlight: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      onPointerEnter={onHighlight}
      onFocus={onHighlight}
      className="group flex min-w-0 items-center gap-4 rounded-xl border bg-background p-4 text-left transition-[border-color,background-color,box-shadow] duration-150 outline-none hover:border-primary/50 hover:shadow-sm focus-visible:border-primary focus-visible:ring-2 focus-visible:ring-ring/40"
    >
      <div className="flex h-14 w-20 shrink-0 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors group-hover:bg-primary/10 group-hover:text-primary group-focus-visible:bg-primary/10 group-focus-visible:text-primary">
        {illustration}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-sm font-semibold">{title}</span>
          {badge}
        </div>
        <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">{description}</p>
        <p className="mt-1.5 text-[11px] font-medium text-muted-foreground/80">{meta}</p>
      </div>
      <ArrowRight className="size-4 shrink-0 text-muted-foreground transition-transform duration-150 group-hover:translate-x-0.5 motion-reduce:transition-none" />
    </button>
  );
}

// Small, theme-aware line drawings: currentColor carries the card's hover
// accent, so they need no separate dark-mode artwork.

function DemoIllustration() {
  return (
    <svg viewBox="0 0 64 40" className="h-10 w-16" fill="none" aria-hidden>
      <path
        d="M17 12 C 26 12, 24 20, 32 20 M17 28 C 26 28, 24 20, 32 20 M44 20 H 50"
        stroke="currentColor"
        strokeOpacity="0.55"
        strokeWidth="1.5"
      />
      <rect
        x="4"
        y="7"
        width="13"
        height="10"
        rx="2.5"
        className="fill-background"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <rect
        x="4"
        y="23"
        width="13"
        height="10"
        rx="2.5"
        className="fill-background"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <rect
        x="32"
        y="15"
        width="12"
        height="10"
        rx="2.5"
        className="fill-background"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <rect
        x="50"
        y="15"
        width="10"
        height="10"
        rx="2.5"
        fill="currentColor"
        fillOpacity="0.25"
        stroke="currentColor"
        strokeWidth="1.5"
      />
    </svg>
  );
}

function ConnectIllustration() {
  return (
    <svg viewBox="0 0 64 40" className="h-10 w-16" fill="none" aria-hidden>
      <ellipse cx="14" cy="11" rx="9" ry="3.5" stroke="currentColor" strokeWidth="1.5" />
      <path
        d="M5 11 V 29 C 5 31, 9 32.5, 14 32.5 C 19 32.5, 23 31, 23 29 V 11"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <path
        d="M5 20 C 5 22, 9 23.5, 14 23.5 C 19 23.5, 23 22, 23 20"
        stroke="currentColor"
        strokeOpacity="0.55"
        strokeWidth="1.5"
      />
      <path
        d="M27 20 H 38"
        stroke="currentColor"
        strokeOpacity="0.55"
        strokeWidth="1.5"
        strokeDasharray="2.5 2.5"
      />
      <path
        d="M35 17 L 38 20 L 35 23"
        stroke="currentColor"
        strokeOpacity="0.55"
        strokeWidth="1.5"
      />
      <rect
        x="42"
        y="9"
        width="17"
        height="9"
        rx="2.5"
        className="fill-background"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <rect
        x="42"
        y="22"
        width="17"
        height="9"
        rx="2.5"
        className="fill-background"
        stroke="currentColor"
        strokeWidth="1.5"
      />
    </svg>
  );
}

function ScratchIllustration() {
  return (
    <svg viewBox="0 0 64 40" className="h-10 w-16" fill="none" aria-hidden>
      <rect
        x="10"
        y="12"
        width="20"
        height="16"
        rx="3"
        className="fill-background"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <path d="M30 20 H 38" stroke="currentColor" strokeOpacity="0.55" strokeWidth="1.5" />
      <rect
        x="38"
        y="12"
        width="16"
        height="16"
        rx="3"
        stroke="currentColor"
        strokeOpacity="0.55"
        strokeWidth="1.5"
        strokeDasharray="3 3"
      />
      <path d="M46 16.5 V 23.5 M42.5 20 H 49.5" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  );
}
