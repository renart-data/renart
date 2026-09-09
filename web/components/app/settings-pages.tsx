import { Link, Outlet } from "@tanstack/react-router";
import { Boxes, Plug, Sliders } from "lucide-react";
import { ComponentType, useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import type { WorkspaceRetentionSettings } from "@/lib/generated/api-types";
import { cn } from "@/lib/utils";
import { PageHeader, AppPage } from "./app-primitives";
import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";
import { SettingsCard, SettingsStatus, PlainFieldGroup, PlainField } from "./settings-form-parts";

type RetentionForm = {
  runMetadataDays: string;
  minimumRunsPerPipeline: string;
  fullLogsDays: string;
  minimumLoggedRunsPerPipeline: string;
  materializationFactsDays: string;
  scheduleHistoryDays: string;
  deploymentDays: string;
  minimumDeploymentsPerPipeline: string;
  temporaryDirectoriesHours: string;
};

const emptyRetentionForm: RetentionForm = {
  runMetadataDays: "",
  minimumRunsPerPipeline: "",
  fullLogsDays: "",
  minimumLoggedRunsPerPipeline: "",
  materializationFactsDays: "",
  scheduleHistoryDays: "",
  deploymentDays: "",
  minimumDeploymentsPerPipeline: "",
  temporaryDirectoriesHours: "",
};

function retentionFormFromSettings(settings: WorkspaceRetentionSettings): RetentionForm {
  return {
    runMetadataDays: String(settings.run_metadata.days),
    minimumRunsPerPipeline: String(settings.run_metadata.minimum_per_pipeline),
    fullLogsDays: String(settings.full_logs.days),
    minimumLoggedRunsPerPipeline: String(settings.full_logs.minimum_per_pipeline),
    materializationFactsDays: String(settings.materialization_facts_days),
    scheduleHistoryDays: String(settings.schedule_history_days),
    deploymentDays: String(settings.deployments.days),
    minimumDeploymentsPerPipeline: String(settings.deployments.minimum_per_pipeline),
    temporaryDirectoriesHours: String(settings.temporary_directories_hours),
  };
}

function parseRetentionForm(form: RetentionForm): WorkspaceRetentionSettings | null {
  const positive = (value: string) => {
    const parsed = Number(value);
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
  };
  const nonNegative = (value: string) => {
    const parsed = Number(value);
    return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : null;
  };
  const runMetadataDays = positive(form.runMetadataDays);
  const minimumRuns = nonNegative(form.minimumRunsPerPipeline);
  const fullLogsDays = positive(form.fullLogsDays);
  const minimumLoggedRuns = nonNegative(form.minimumLoggedRunsPerPipeline);
  const materializationFactsDays = positive(form.materializationFactsDays);
  const scheduleHistoryDays = positive(form.scheduleHistoryDays);
  const deploymentDays = positive(form.deploymentDays);
  const minimumDeployments = nonNegative(form.minimumDeploymentsPerPipeline);
  const temporaryDirectoriesHours = positive(form.temporaryDirectoriesHours);
  if (
    runMetadataDays === null ||
    minimumRuns === null ||
    fullLogsDays === null ||
    minimumLoggedRuns === null ||
    materializationFactsDays === null ||
    scheduleHistoryDays === null ||
    deploymentDays === null ||
    minimumDeployments === null ||
    temporaryDirectoriesHours === null
  ) {
    return null;
  }
  return {
    run_metadata: { days: runMetadataDays, minimum_per_pipeline: minimumRuns },
    full_logs: { days: fullLogsDays, minimum_per_pipeline: minimumLoggedRuns },
    materialization_facts_days: materializationFactsDays,
    schedule_history_days: scheduleHistoryDays,
    deployments: { days: deploymentDays, minimum_per_pipeline: minimumDeployments },
    temporary_directories_hours: temporaryDirectoriesHours,
  };
}

const projectSections = [
  { id: "general", label: "General", icon: Sliders, to: "/project/general" },
  { id: "environments", label: "Environments", icon: Boxes, to: "/project/environments" },
  { id: "connections", label: "Connections", icon: Plug, to: "/project/connections" },
] as const;

export function AppProjectSettingsShell() {
  const { workspaceConfig } = useWorkspaceSettingsData();
  const projectName = workspaceConfig?.project_name || "Project";

  return (
    <SettingsShell
      title="Project settings"
      subtitle={`${projectName} defaults, connections, and environments`}
      eyebrow={`Project · ${projectName}`}
      sections={projectSections}
    />
  );
}

function SettingsShell({
  title,
  subtitle,
  eyebrow,
  sections,
}: {
  title: string;
  subtitle: string;
  eyebrow: string;
  sections: ReadonlyArray<{
    id: string;
    label: string;
    icon: ComponentType<{ className?: string }>;
    to: string;
  }>;
}) {
  const { navigation } = useWorkbench();
  const workbenchEnabled = Boolean(navigation?.workbench);
  const activeSection = sections.find(
    (section) =>
      section.id === (navigation?.tool === "project-settings" ? "general" : navigation?.tool),
  );

  return (
    <AppPage>
      {workbenchEnabled &&
      activeSection?.id !== "connections" &&
      activeSection?.id !== "environments" ? (
        <WorkbenchPortal slot="context">
          <AppContextSidebarFrame
            title={activeSection?.label ?? title}
            subtitle={activeSection ? eyebrow : subtitle}
          >
            <div className="flex flex-col gap-1 p-2">
              <p className="px-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                Project configuration
              </p>
              {sections.map((section) => (
                <SettingsSideLink key={section.id} section={section} />
              ))}
            </div>
          </AppContextSidebarFrame>
        </WorkbenchPortal>
      ) : !workbenchEnabled ? (
        <PageHeader title={title} subtitle={subtitle} />
      ) : null}
      <div
        className={cn(
          "min-h-0 min-w-0 flex-1 overflow-hidden",
          !workbenchEnabled &&
            "grid grid-cols-1 gap-3 px-3 pb-3 md:grid-cols-[16rem_minmax(0,1fr)]",
        )}
      >
        {!workbenchEnabled ? (
          <aside className="hidden min-h-0 md:block">
            <div className="sticky top-0 flex flex-col gap-1">
              <div className="px-2 pb-2 text-xs font-medium text-muted-foreground">{eyebrow}</div>
              {sections.map((section) => (
                <SettingsSideLink key={section.id} section={section} />
              ))}
            </div>
          </aside>
        ) : null}
        <div className="h-full min-h-0 min-w-0 overflow-hidden">
          {!workbenchEnabled ? (
            <ScrollArea
              className="mb-3 md:hidden"
              horizontalScrollBarClassName="hidden"
              viewportClassName="w-full"
            >
              <div className="flex gap-2 pb-1">
                {sections.map((section) => (
                  <SettingsPillLink key={section.id} section={section} />
                ))}
              </div>
            </ScrollArea>
          ) : null}
          <ScrollArea
            data-testid="project-settings-scroll"
            className="h-full min-h-0"
            viewportClassName="[&>div]:!block [&>div]:w-full"
          >
            <div className={cn("mx-auto w-full min-w-0 max-w-4xl", workbenchEnabled && "p-3")}>
              <Outlet />
            </div>
          </ScrollArea>
        </div>
      </div>
    </AppPage>
  );
}

function SettingsSideLink({
  section,
}: {
  section: { label: string; icon: ComponentType<{ className?: string }>; to: string };
}) {
  return (
    <Link
      to={section.to}
      className="flex h-9 items-center gap-2 rounded-md px-2.5 text-sm text-muted-foreground hover:bg-background hover:text-foreground"
      activeProps={{ className: "bg-background text-foreground shadow-sm font-medium" }}
    >
      <section.icon className="size-4" />
      {section.label}
    </Link>
  );
}

function SettingsPillLink({
  section,
}: {
  section: { label: string; icon: ComponentType<{ className?: string }>; to: string };
}) {
  return (
    <Link to={section.to} className="shrink-0" activeProps={{ className: "text-primary" }}>
      {({ isActive }) => (
        <Badge variant={isActive ? "default" : "outline"} className="h-8 px-3">
          <section.icon className="size-3.5" />
          {section.label}
        </Badge>
      )}
    </Link>
  );
}

export function AppProjectGeneralPage() {
  const {
    handleUpdateWorkspaceEnvironment,
    handleUpdateWorkspaceProject,
    loadWorkspaceConfig,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigBusy,
    workspaceConfigLoading,
    workspaceConfigStatusMessage,
    workspaceConfigStatusTone,
  } = useWorkspaceSettingsData();
  const [projectName, setProjectName] = useState("");
  const [defaultEnvironment, setDefaultEnvironment] = useState("");
  const [retentionForm, setRetentionForm] = useState<RetentionForm>(emptyRetentionForm);

  useEffect(() => {
    void loadWorkspaceConfig();
  }, [loadWorkspaceConfig]);

  useEffect(() => {
    setProjectName(workspaceConfig?.project_name || "");
  }, [workspaceConfig?.project_name]);

  useEffect(() => {
    if (workspaceConfig?.retention) {
      setRetentionForm(retentionFormFromSettings(workspaceConfig.retention));
    }
  }, [workspaceConfig?.retention]);

  useEffect(() => {
    setDefaultEnvironment(
      workspaceConfig?.default_environment || normalizedConfigEnvironments[0]?.name || "",
    );
  }, [normalizedConfigEnvironments, workspaceConfig?.default_environment]);

  const selectedDefaultEnv = normalizedConfigEnvironments.find(
    (environment) => environment.name === defaultEnvironment,
  );
  const projectNameDirty = projectName.trim() !== (workspaceConfig?.project_name || "");
  const retentionSettings = parseRetentionForm(retentionForm);
  const retentionDirty = Boolean(
    retentionSettings &&
    workspaceConfig?.retention &&
    JSON.stringify(retentionSettings) !== JSON.stringify(workspaceConfig.retention),
  );

  return (
    <div className="flex flex-col gap-4">
      <SettingsStatus message={workspaceConfigStatusMessage} tone={workspaceConfigStatusTone} />
      <SettingsCard
        title="Project"
        action={
          <Button
            size="sm"
            disabled={workspaceConfigBusy || !projectName.trim() || !projectNameDirty}
            onClick={() => void handleUpdateWorkspaceProject({ name: projectName.trim() })}
          >
            Save name
          </Button>
        }
      >
        <PlainFieldGroup className="md:grid-cols-2">
          <PlainField>
            <Label>Project name</Label>
            <Input
              value={projectName}
              onChange={(event) => setProjectName(event.target.value)}
              placeholder="data_platform"
            />
          </PlainField>
          <ReadonlyField
            label="Project id"
            value={workspaceConfig?.project_id || "Assigned on first load"}
            mono
          />
          <ReadonlyField
            label="Workspace path"
            value={workspaceConfig?.workspace_path || "Loading..."}
            mono
          />
          <ReadonlyField label="Config file" value={workspaceConfig?.path || ".bruin.yml"} mono />
        </PlainFieldGroup>
      </SettingsCard>
      <SettingsCard title="Features">
        <div className="flex items-center justify-between gap-4">
          <div>
            <Label>Ingestr sources</Label>
            <p className="mt-1 text-xs text-muted-foreground">
              Offer ingestr source connection types and asset options. Off by default; pipelines
              that already contain ingestr assets keep working either way.
            </p>
          </div>
          <Switch
            checked={Boolean(workspaceConfig?.features?.ingestr)}
            disabled={workspaceConfigBusy || workspaceConfigLoading}
            onCheckedChange={(checked) =>
              void handleUpdateWorkspaceProject({ features: { ingestr: checked } })
            }
            aria-label="Enable ingestr sources"
          />
        </div>
      </SettingsCard>
      <SettingsCard
        title="Local history retention"
        description="Daily housekeeping removes records only after both the age limit and the per-pipeline minimum allow it. Active and referenced state is always protected."
        action={
          <Button
            size="sm"
            disabled={workspaceConfigBusy || !retentionSettings || !retentionDirty}
            onClick={() => {
              if (!retentionSettings) return;
              void handleUpdateWorkspaceProject({ retention: retentionSettings });
            }}
          >
            Save retention
          </Button>
        }
      >
        <PlainFieldGroup className="md:grid-cols-2">
          <RetentionNumberField
            label="Run metadata (days)"
            description="Run context, steps, and reviewed plans."
            value={retentionForm.runMetadataDays}
            onChange={(value) =>
              setRetentionForm((current) => ({ ...current, runMetadataDays: value }))
            }
          />
          <RetentionNumberField
            label="Minimum runs per pipeline"
            description="Newest runs retained even after the age limit."
            allowZero
            value={retentionForm.minimumRunsPerPipeline}
            onChange={(value) =>
              setRetentionForm((current) => ({ ...current, minimumRunsPerPipeline: value }))
            }
          />
          <RetentionNumberField
            label="Full logs (days)"
            description="Verbose output may expire before run metadata."
            value={retentionForm.fullLogsDays}
            onChange={(value) =>
              setRetentionForm((current) => ({ ...current, fullLogsDays: value }))
            }
          />
          <RetentionNumberField
            label="Minimum logged runs per pipeline"
            description="Newest runs whose complete output is retained."
            allowZero
            value={retentionForm.minimumLoggedRunsPerPipeline}
            onChange={(value) =>
              setRetentionForm((current) => ({
                ...current,
                minimumLoggedRunsPerPipeline: value,
              }))
            }
          />
          <RetentionNumberField
            label="Materialization facts (days)"
            description="Raw facts only; compact freshness evidence remains."
            value={retentionForm.materializationFactsDays}
            onChange={(value) =>
              setRetentionForm((current) => ({ ...current, materializationFactsDays: value }))
            }
          />
          <RetentionNumberField
            label="Schedule history (days)"
            description="Completed occurrences and archived schedule tombstones."
            value={retentionForm.scheduleHistoryDays}
            onChange={(value) =>
              setRetentionForm((current) => ({ ...current, scheduleHistoryDays: value }))
            }
          />
          <RetentionNumberField
            label="Unreferenced deployments (days)"
            description="Pinned, current, and run-referenced deployments remain."
            value={retentionForm.deploymentDays}
            onChange={(value) =>
              setRetentionForm((current) => ({ ...current, deploymentDays: value }))
            }
          />
          <RetentionNumberField
            label="Minimum deployments per pipeline"
            description="Newest snapshots retained even after the age limit."
            allowZero
            value={retentionForm.minimumDeploymentsPerPipeline}
            onChange={(value) =>
              setRetentionForm((current) => ({
                ...current,
                minimumDeploymentsPerPipeline: value,
              }))
            }
          />
          <RetentionNumberField
            label="Abandoned temporary folders (hours)"
            description="Only Renart-owned folders left by an earlier process."
            value={retentionForm.temporaryDirectoriesHours}
            onChange={(value) =>
              setRetentionForm((current) => ({ ...current, temporaryDirectoriesHours: value }))
            }
          />
        </PlainFieldGroup>
      </SettingsCard>
      <SettingsCard
        title="Default environment"
        action={
          <Button
            size="sm"
            disabled={workspaceConfigBusy || workspaceConfigLoading || !selectedDefaultEnv}
            onClick={() => {
              if (!selectedDefaultEnv) return;
              void handleUpdateWorkspaceEnvironment({
                name: selectedDefaultEnv.name,
                schema_prefix: selectedDefaultEnv.schema_prefix,
                set_as_default: true,
              });
            }}
          >
            Save default
          </Button>
        }
      >
        <PlainFieldGroup>
          <PlainField>
            <Label>Environment</Label>
            <Select value={defaultEnvironment || undefined} onValueChange={setDefaultEnvironment}>
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Select environment" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {normalizedConfigEnvironments.map((environment) => (
                    <SelectItem key={environment.name} value={environment.name}>
                      {environment.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </PlainField>
        </PlainFieldGroup>
      </SettingsCard>
    </div>
  );
}

function ReadonlyField({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <PlainField>
      <Label>{label}</Label>
      <Input value={value} readOnly className={mono ? "font-mono" : undefined} />
    </PlainField>
  );
}

function RetentionNumberField({
  label,
  description,
  value,
  allowZero = false,
  onChange,
}: {
  label: string;
  description: string;
  value: string;
  allowZero?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <PlainField>
      <Label>{label}</Label>
      <Input
        type="number"
        inputMode="numeric"
        min={allowZero ? 0 : 1}
        step={1}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
      <p className="text-xs text-muted-foreground">{description}</p>
    </PlainField>
  );
}

export { AppProjectConnectionsPage, AppProjectEnvironmentsPage } from "./workspace-settings-pages";
