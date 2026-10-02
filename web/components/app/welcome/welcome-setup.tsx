import {
  ArrowLeft,
  Check,
  CircleAlert,
  Database,
  FolderPlus,
  Globe2,
  HardDrive,
  LoaderCircle,
  Play,
  Rocket,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ProjectTemplateInfo } from "@/lib/generated/api-types";
import { cn } from "@/lib/utils";

import { shortPath, type WelcomePath } from "./welcome-flow";

export function WelcomeSetup({
  path,
  demos,
  templateId,
  inPlace,
  workspacePath,
  targetFolder,
  projectName,
  parentDir,
  parentDirLoading,
  busy,
  error,
  onSelectTemplate,
  onProjectNameChange,
  onChooseLocation,
  onBack,
  onSubmit,
}: {
  path: WelcomePath;
  demos: ProjectTemplateInfo[];
  templateId: string;
  inPlace: boolean;
  workspacePath: string;
  // The folder the template creates inside an open workspace, e.g. "product_analytics".
  targetFolder: string;
  projectName: string;
  parentDir: string;
  parentDirLoading: boolean;
  busy: boolean;
  error: string | null;
  onSelectTemplate: (templateId: string) => void;
  onProjectNameChange: (name: string) => void;
  onChooseLocation: () => void;
  onBack: () => void;
  onSubmit: () => void;
}) {
  const [editingTarget, setEditingTarget] = useState(false);
  // A rejected name or location needs the fields, not the summary line.
  const showTargetFields = editingTarget || Boolean(error);
  // A failed create returns here from the run step, which resets the scroll
  // position; keep the error beside the create action in view.
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (error) errorRef.current?.scrollIntoView({ block: "nearest" });
  }, [error]);
  const trimmedName = projectName.trim();
  const canSubmit =
    !busy &&
    (inPlace || (trimmedName !== "" && !parentDirLoading)) &&
    (path !== "demo" || templateId !== "");

  return (
    <>
      <div>
        <h2 className="text-lg font-semibold tracking-tight">
          {path === "demo"
            ? "Pick a demo"
            : path === "import"
              ? "Where should the project live?"
              : "Name your project"}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {path === "demo"
            ? "Each demo is a small pipeline of plain SQL and YAML files. Renart creates it and runs it once."
            : path === "import"
              ? "Renart creates the project, then connects to your database."
              : "Renart creates a pipeline with one example SQL asset on local DuckDB."}
        </p>
      </div>

      {path === "demo" ? (
        <div
          role="radiogroup"
          aria-label="Demo pipeline"
          className="grid overflow-hidden rounded-xl border bg-background"
        >
          {demos.map((demo) => {
            const selected = demo.id === templateId;
            return (
              <button
                key={demo.id}
                type="button"
                role="radio"
                aria-checked={selected}
                onClick={() => onSelectTemplate(demo.id)}
                className={cn(
                  "flex min-w-0 items-center gap-3 border-b px-3.5 py-2.5 text-left transition-colors outline-none last:border-b-0 hover:bg-muted/50 focus-visible:bg-muted/60",
                  selected && "bg-primary/5 hover:bg-primary/10",
                )}
              >
                <span
                  aria-hidden
                  className={cn(
                    "flex size-4 shrink-0 items-center justify-center rounded-full border transition-colors",
                    selected
                      ? "border-primary bg-primary text-primary-foreground"
                      : "border-muted-foreground/40 text-transparent",
                  )}
                >
                  <Check className="size-2.5" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{demo.title}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {demo.category} · {demo.asset_names.length} assets
                  </span>
                </span>
                {demo.offline ? (
                  <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
                    <HardDrive className="size-3" />
                    Local data
                  </span>
                ) : (
                  <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
                    <Globe2 className="size-3" />
                    Uses the internet
                  </span>
                )}
              </button>
            );
          })}
        </div>
      ) : null}

      <div className="rounded-xl border bg-background p-4">
        {inPlace ? (
          <p className="text-sm text-muted-foreground" title={workspacePath || undefined}>
            Creates{" "}
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
              {targetFolder ? `${targetFolder}/` : "the project files"}
            </code>{" "}
            in this workspace.
          </p>
        ) : showTargetFields ? (
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="welcome-project-name">Project name</Label>
              <Input
                id="welcome-project-name"
                value={projectName}
                onChange={(event) => onProjectNameChange(event.target.value)}
                autoFocus
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="welcome-project-location">Location</Label>
              <Button
                id="welcome-project-location"
                type="button"
                variant="outline"
                className="min-w-0 justify-start"
                aria-label="Choose project location"
                title={parentDir || undefined}
                disabled={parentDirLoading}
                onClick={onChooseLocation}
              >
                {parentDirLoading ? (
                  <LoaderCircle data-icon="inline-start" className="animate-spin" />
                ) : (
                  <FolderPlus data-icon="inline-start" />
                )}
                <span className="truncate font-mono text-xs">
                  {parentDirLoading
                    ? "Loading suggested location..."
                    : parentDir || "Choose a directory"}
                </span>
              </Button>
              <p className="text-xs text-muted-foreground">
                Renart creates a {trimmedName || "project"} folder here with its own Git repository.
              </p>
            </div>
          </div>
        ) : (
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
            <span className="min-w-0">
              Creates{" "}
              <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
                {trimmedName || "project"}
              </code>{" "}
              in{" "}
              <span className="font-mono text-xs" title={parentDir || undefined}>
                {parentDirLoading ? "..." : shortPath(parentDir || "~", 2)}
              </span>{" "}
              with its own Git repository.
            </span>
            <Button
              variant="link"
              size="sm"
              className="h-auto p-0"
              onClick={() => setEditingTarget(true)}
            >
              Change
            </Button>
          </div>
        )}
      </div>

      {error ? (
        <Alert ref={errorRef} variant="destructive">
          <CircleAlert />
          <AlertTitle>Project could not be created</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      <div className="flex items-center justify-between gap-2">
        <Button variant="ghost" onClick={onBack} disabled={busy}>
          <ArrowLeft data-icon="inline-start" />
          Back
        </Button>
        <Button onClick={onSubmit} disabled={!canSubmit}>
          {busy ? (
            <LoaderCircle data-icon="inline-start" className="animate-spin" />
          ) : path === "demo" ? (
            <Play data-icon="inline-start" />
          ) : path === "import" ? (
            <Database data-icon="inline-start" />
          ) : (
            <Rocket data-icon="inline-start" />
          )}
          {path === "demo"
            ? "Create and run"
            : path === "import"
              ? "Create and connect"
              : "Create project"}
        </Button>
      </div>
    </>
  );
}
