import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import {
  Boxes,
  CheckCircle2,
  Copy,
  KeyRound,
  LoaderCircle,
  LockKeyhole,
  Pencil,
  Plug,
  Plus,
  Sliders,
  Trash2,
  UnlockKeyhole,
} from "lucide-react";
import { ComponentType, HTMLAttributes, ReactNode, useEffect, useMemo, useState } from "react";

import { WorkspaceConnectionFormFields } from "@/components/workspace-connection-form-fields";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DelimitedCard,
  DelimitedCardAction,
  DelimitedCardContent,
  DelimitedCardDescription,
  DelimitedCardHeader,
  DelimitedCardTitle,
} from "@/components/ui/delimited-card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldTitle,
} from "@/components/ui/field";
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
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { useWorkspaceConnectionForm } from "@/hooks/use-workspace-connection-form";
import { useWorkspaceEnvironmentForm } from "@/hooks/use-workspace-environment-form";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { testWorkspaceConnection } from "@/lib/api-config";
import { useIngestrEnabled, visibleConnectionTypes } from "@/lib/features";
import type { EnvironmentPolicy, WorkspaceRetentionSettings } from "@/lib/generated/api-types";
import {
  buildConnectionFieldDefaults,
  buildConnectionSecretChanges,
} from "@/lib/settings-form-utils";
import type { WorkspaceConfigConnection, WorkspaceConfigEnvironment } from "@/lib/types";
import { cn } from "@/lib/utils";

import { IntegrationBadge, PageHeader, AppPage } from "./app-primitives";
import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";

const emptyPolicy: EnvironmentPolicy = {
  protected: false,
  deployed_only: false,
  confirm_destructive: false,
};

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

function policiesEqual(left: EnvironmentPolicy, right: EnvironmentPolicy) {
  return (
    Boolean(left.protected) === Boolean(right.protected) &&
    Boolean(left.deployed_only) === Boolean(right.deployed_only) &&
    Boolean(left.confirm_destructive) === Boolean(right.confirm_destructive)
  );
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
      {workbenchEnabled ? (
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
      ) : (
        <PageHeader title={title} subtitle={subtitle} />
      )}
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

type EnvironmentSheetState =
  | { mode: "create" }
  | { mode: "clone"; name: string }
  | { mode: "edit"; name: string };

export function AppProjectEnvironmentsPage() {
  const settings = useWorkspaceSettingsData();
  const {
    loadWorkspaceConfig,
    loadWorkspaceEnvironmentPolicy,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigStatusMessage,
    workspaceConfigStatusTone,
    workspaceEnvironmentPolicies,
  } = settings;
  const [sheetState, setSheetState] = useState<EnvironmentSheetState | null>(null);

  useEffect(() => {
    void loadWorkspaceConfig();
  }, [loadWorkspaceConfig]);

  useEffect(() => {
    for (const environment of normalizedConfigEnvironments) {
      if (!workspaceEnvironmentPolicies[environment.name]) {
        void loadWorkspaceEnvironmentPolicy(environment.name);
      }
    }
  }, [loadWorkspaceEnvironmentPolicy, normalizedConfigEnvironments, workspaceEnvironmentPolicies]);

  return (
    <div className="flex min-h-0 flex-col gap-4">
      <SettingsStatus message={workspaceConfigStatusMessage} tone={workspaceConfigStatusTone} />
      <SettingsCard
        title="Environments"
        description="Each environment carries its own connections and guardrails."
        action={
          <Button size="sm" onClick={() => setSheetState({ mode: "create" })}>
            <Plus data-icon="inline-start" />
            New environment
          </Button>
        }
      >
        <div className="flex flex-col">
          {normalizedConfigEnvironments.length === 0 ? (
            <p className="text-sm text-muted-foreground">No environments are configured yet.</p>
          ) : (
            normalizedConfigEnvironments.map((environment) => (
              <EnvironmentRow
                key={environment.name}
                environment={environment}
                defaultEnvironment={workspaceConfig?.default_environment}
                policy={workspaceEnvironmentPolicies[environment.name]}
                onSelect={() => setSheetState({ mode: "edit", name: environment.name })}
              />
            ))
          )}
        </div>
      </SettingsCard>
      <EnvironmentSheet state={sheetState} onStateChange={setSheetState} settings={settings} />
    </div>
  );
}

function EnvironmentSheet({
  state,
  onStateChange,
  settings,
}: {
  state: EnvironmentSheetState | null;
  onStateChange: (state: EnvironmentSheetState | null) => void;
  settings: ReturnType<typeof useWorkspaceSettingsData>;
}) {
  const {
    handleCloneWorkspaceEnvironment,
    handleCreateWorkspaceEnvironment,
    handleDeleteWorkspaceEnvironment,
    handleUpdateWorkspaceEnvironment,
    handleUpdateWorkspaceEnvironmentPolicy,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigBusy,
    workspaceConfigStatusMessage,
    workspaceConfigStatusTone,
    workspaceEnvironmentPolicies,
  } = settings;
  const mode = state?.mode ?? "edit";
  const selectedEnvironmentName = state?.mode === "create" ? null : (state?.name ?? null);

  const { activeEnvironment, environmentForm, handleDelete, handleSave, setEnvironmentForm } =
    useWorkspaceEnvironmentForm({
      defaultEnvironment: workspaceConfig?.default_environment,
      environments: normalizedConfigEnvironments,
      mode,
      onCloneEnvironment: handleCloneWorkspaceEnvironment,
      onCreateEnvironment: handleCreateWorkspaceEnvironment,
      onDeleteEnvironment: handleDeleteWorkspaceEnvironment,
      onModeChange: () => {},
      onSelectedEnvironmentChange: () => {},
      onUpdateEnvironment: handleUpdateWorkspaceEnvironment,
      selectedEnvironmentName,
    });

  const editName = state?.mode === "edit" ? state.name : null;
  const storedPolicy = (editName ? workspaceEnvironmentPolicies[editName] : null) ?? emptyPolicy;
  const [policyDraft, setPolicyDraft] = useState<EnvironmentPolicy>(emptyPolicy);

  useEffect(() => {
    setPolicyDraft(storedPolicy);
  }, [editName, storedPolicy]);

  const close = () => onStateChange(null);

  const save = async () => {
    if (!state) return;
    try {
      await handleSave();
      if (state.mode === "edit") {
        const nextName = environmentForm.name.trim() || state.name;
        if (!policiesEqual(policyDraft, storedPolicy)) {
          await handleUpdateWorkspaceEnvironmentPolicy(nextName, policyDraft);
        }
      }
      close();
    } catch {
      // Keep the sheet open; the error alert below shows what failed.
    }
  };

  const remove = async () => {
    try {
      await handleDelete();
      close();
    } catch {
      // Keep the sheet open; the error alert below shows what failed.
    }
  };

  const title =
    mode === "create"
      ? "New environment"
      : mode === "clone"
        ? `Clone ${environmentForm.cloneSourceName || "environment"}`
        : activeEnvironment
          ? activeEnvironment.name
          : "Environment";
  const description =
    mode === "create"
      ? "Add an environment to this project."
      : mode === "clone"
        ? "Copy an environment including its connections and guardrails."
        : "Rename, set defaults, and adjust guardrails.";

  return (
    <Sheet open={state !== null} onOpenChange={(open) => !open && close()}>
      <SheetContent className="w-full sm:max-w-lg">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            <Boxes className="size-4 text-primary" />
            {title}
          </SheetTitle>
          <SheetDescription>{description}</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-auto px-4">
          <PlainFieldGroup>
            {workspaceConfigStatusTone === "error" ? (
              <SettingsStatus
                message={workspaceConfigStatusMessage}
                tone={workspaceConfigStatusTone}
              />
            ) : null}
            {mode === "clone" ? (
              <PlainField>
                <Label>Source</Label>
                <Select
                  value={environmentForm.cloneSourceName || undefined}
                  onValueChange={(value) => {
                    const source = normalizedConfigEnvironments.find(
                      (environment) => environment.name === value,
                    );
                    setEnvironmentForm((current) => ({
                      ...current,
                      cloneSourceName: value,
                      schemaPrefix: source?.schema_prefix ?? current.schemaPrefix,
                    }));
                  }}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="Select source" />
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
            ) : null}
            <PlainField>
              <Label>Name</Label>
              <Input
                value={environmentForm.name}
                onChange={(event) =>
                  setEnvironmentForm((current) => ({ ...current, name: event.target.value }))
                }
                placeholder="prod"
              />
            </PlainField>
            <PlainField>
              <Label>Schema prefix</Label>
              <Input
                value={environmentForm.schemaPrefix}
                onChange={(event) =>
                  setEnvironmentForm((current) => ({
                    ...current,
                    schemaPrefix: event.target.value,
                  }))
                }
                placeholder="analytics_"
              />
              <p className="text-xs text-muted-foreground">
                Prepended to schema names at runtime. For example,
                <span className="font-mono"> dev_</span> turns
                <span className="font-mono"> analytics.orders</span> into
                <span className="font-mono"> dev_analytics.orders</span> while the asset name stays
                unchanged.
              </p>
            </PlainField>
            <Field orientation="horizontal">
              <FieldContent>
                <FieldTitle>Default environment</FieldTitle>
                <FieldDescription>
                  Use this environment when no explicit environment is selected.
                </FieldDescription>
              </FieldContent>
              <Switch
                checked={environmentForm.setAsDefault}
                onCheckedChange={(checked) =>
                  setEnvironmentForm((current) => ({ ...current, setAsDefault: checked }))
                }
              />
            </Field>
            {mode === "edit" ? (
              <div className="grid gap-3 border-t pt-4">
                <div>
                  <h3 className="text-sm font-medium">Execution policy</h3>
                  <p className="text-sm text-muted-foreground">
                    renart-only guardrails stored in .renart/environments.yml, applied on save.
                  </p>
                </div>
                <EnvironmentPolicyFields
                  policy={policyDraft}
                  disabled={workspaceConfigBusy}
                  onChange={setPolicyDraft}
                />
              </div>
            ) : null}
            {mode === "edit" && activeEnvironment ? (
              <div className="grid gap-2 border-t pt-4">
                <h3 className="text-sm font-medium">Connections</h3>
                {activeEnvironment.connections.length === 0 ? (
                  <p className="text-sm text-muted-foreground">
                    No connections in this environment.
                  </p>
                ) : (
                  <div className="flex flex-wrap gap-2">
                    {activeEnvironment.connections.map((connection) => (
                      <Badge key={connection.name} variant="outline" className="gap-1.5 font-mono">
                        {connection.name}
                        <span className="font-sans text-muted-foreground">{connection.type}</span>
                      </Badge>
                    ))}
                  </div>
                )}
                <p className="text-xs text-muted-foreground">
                  Manage them in the{" "}
                  <Link
                    to="/project/connections"
                    search={{}}
                    className="underline underline-offset-2"
                  >
                    Connections
                  </Link>{" "}
                  tab.
                </p>
              </div>
            ) : null}
          </PlainFieldGroup>
        </div>
        <SheetFooter>
          <div className="flex w-full items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              {mode === "edit" && activeEnvironment ? (
                <>
                  <ConfirmDeleteButton
                    disabled={workspaceConfigBusy}
                    label="Delete"
                    onConfirm={() => void remove()}
                  />
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={workspaceConfigBusy}
                    onClick={() => onStateChange({ mode: "clone", name: activeEnvironment.name })}
                  >
                    <Copy data-icon="inline-start" />
                    Clone
                  </Button>
                </>
              ) : null}
            </div>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="outline" disabled={workspaceConfigBusy} onClick={close}>
                Cancel
              </Button>
              <Button
                size="sm"
                disabled={workspaceConfigBusy || !environmentForm.name.trim()}
                onClick={() => void save()}
              >
                {mode === "create"
                  ? "Create environment"
                  : mode === "clone"
                    ? "Clone environment"
                    : "Save changes"}
              </Button>
            </div>
          </div>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

type ConnectionSheetState =
  | { mode: "create"; environment: string | null }
  | { mode: "edit"; environment: string; connection: string };

export function AppProjectConnectionsPage({
  selectedEnvironment,
  selectedConnection,
}: {
  selectedEnvironment?: string;
  selectedConnection?: string;
}) {
  const navigate = useNavigate();
  const settings = useWorkspaceSettingsData();
  const {
    loadWorkspaceConfig,
    loadWorkspaceEnvironmentPolicy,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigStatusMessage,
    workspaceConfigStatusTone,
    workspaceEnvironmentPolicies,
  } = settings;
  const [sheetState, setSheetState] = useState<ConnectionSheetState | null>(null);

  useEffect(() => {
    void loadWorkspaceConfig();
  }, [loadWorkspaceConfig]);

  useEffect(() => {
    for (const environment of normalizedConfigEnvironments) {
      if (!workspaceEnvironmentPolicies[environment.name]) {
        void loadWorkspaceEnvironmentPolicy(environment.name);
      }
    }
  }, [loadWorkspaceEnvironmentPolicy, normalizedConfigEnvironments, workspaceEnvironmentPolicies]);

  useEffect(() => {
    if (!selectedConnection) return;
    const preferredEnvironment = normalizedConfigEnvironments.find(
      (environment) =>
        environment.name === selectedEnvironment &&
        environment.connections.some((connection) => connection.name === selectedConnection),
    );
    const environment =
      preferredEnvironment ??
      normalizedConfigEnvironments.find((item) =>
        item.connections.some((connection) => connection.name === selectedConnection),
      );
    if (!environment) return;
    setSheetState((current) =>
      current?.mode === "edit" &&
      current.environment === environment.name &&
      current.connection === selectedConnection
        ? current
        : { mode: "edit", environment: environment.name, connection: selectedConnection },
    );
  }, [normalizedConfigEnvironments, selectedConnection, selectedEnvironment]);

  const closeSheet = () => {
    setSheetState(null);
    if (selectedConnection || selectedEnvironment) {
      void navigate({ to: "/project/connections", search: {}, replace: true });
    }
  };

  return (
    <div className="flex min-h-0 flex-col gap-4">
      <SettingsStatus message={workspaceConfigStatusMessage} tone={workspaceConfigStatusTone} />
      <SecretBindingsAlert message={workspaceConfig?.secret_bindings_error} />
      {workspaceConfig?.secret_vault ? <LocalVaultCard settings={settings} /> : null}
      {normalizedConfigEnvironments.length === 0 ? (
        <SettingsCard title="Connections">
          <p className="text-sm text-muted-foreground">
            Create an environment first; connections always belong to one.
          </p>
        </SettingsCard>
      ) : (
        normalizedConfigEnvironments.map((environment) => (
          <SettingsCard
            key={environment.name}
            title={
              <span className="flex items-center gap-2">
                <span className="font-mono">{environment.name}</span>
                {environment.name === workspaceConfig?.default_environment ? (
                  <Badge variant="secondary">Default</Badge>
                ) : null}
                {workspaceEnvironmentPolicies[environment.name]?.protected ? (
                  <Badge variant="outline" className="text-destructive">
                    Protected
                  </Badge>
                ) : null}
              </span>
            }
            description={`${environment.connections.length} connection${environment.connections.length === 1 ? "" : "s"}`}
            action={
              <Button
                size="sm"
                variant="outline"
                onClick={() => setSheetState({ mode: "create", environment: environment.name })}
              >
                <Plus data-icon="inline-start" />
                Add
              </Button>
            }
          >
            {environment.connections.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No connections in this environment yet.
              </p>
            ) : (
              <div className="flex flex-col">
                {environment.connections.map((connection) => (
                  <ConnectionRow
                    key={connection.name}
                    connection={connection}
                    onSelect={() =>
                      setSheetState({
                        mode: "edit",
                        environment: environment.name,
                        connection: connection.name,
                      })
                    }
                  />
                ))}
              </div>
            )}
          </SettingsCard>
        ))
      )}
      <ConnectionSheet state={sheetState} onClose={closeSheet} settings={settings} />
    </div>
  );
}

type LocalVaultDialogMode = "initialize" | "unlock" | "change";

function LocalVaultCard({ settings }: { settings: ReturnType<typeof useWorkspaceSettingsData> }) {
  const {
    handleChangeLocalVaultPassphrase,
    handleInitializeLocalVault,
    handleLockLocalVault,
    handleUnlockLocalVault,
    workspaceConfig,
    workspaceConfigBusy,
  } = settings;
  const vault = workspaceConfig?.secret_vault;
  const [dialogMode, setDialogMode] = useState<LocalVaultDialogMode | null>(null);
  const [passphrase, setPassphrase] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState("");

  if (!vault) {
    return null;
  }

  const closeDialog = () => {
    setDialogMode(null);
    setPassphrase("");
    setConfirmation("");
    setError("");
  };
  const needsConfirmation = dialogMode === "initialize" || dialogMode === "change";
  const passphraseValid =
    passphrase.length > 0 &&
    (!needsConfirmation || (Array.from(passphrase).length >= 12 && passphrase === confirmation));

  const submit = async () => {
    if (!dialogMode || !passphraseValid) {
      return;
    }
    setError("");
    try {
      if (dialogMode === "initialize") {
        await handleInitializeLocalVault(passphrase);
      } else if (dialogMode === "unlock") {
        await handleUnlockLocalVault(passphrase);
      } else {
        await handleChangeLocalVaultPassphrase(passphrase);
      }
      closeDialog();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not update the encrypted vault.");
    }
  };

  const badgeVariant = vault.state === "unlocked" ? "secondary" : "outline";
  const statusLabel =
    vault.state === "uninitialized"
      ? "Not set up"
      : vault.state === "unavailable"
        ? "Unavailable"
        : vault.state === "unlocked"
          ? "Unlocked"
          : "Locked";

  return (
    <>
      <SettingsCard
        title={
          <span className="flex items-center gap-2">
            <KeyRound className="size-4 text-primary" />
            Encrypted vault
            <Badge variant={badgeVariant}>{statusLabel}</Badge>
          </span>
        }
        description="A passphrase-protected credential fallback for SSH, headless sessions, and systems without a credential service."
        action={
          vault.state === "uninitialized" ? (
            <Button
              size="sm"
              variant="outline"
              disabled={workspaceConfigBusy}
              onClick={() => setDialogMode("initialize")}
            >
              Set up
            </Button>
          ) : vault.state === "locked" ? (
            <Button
              size="sm"
              variant="outline"
              disabled={workspaceConfigBusy}
              onClick={() => setDialogMode("unlock")}
            >
              <UnlockKeyhole data-icon="inline-start" />
              Unlock
            </Button>
          ) : vault.state === "unlocked" ? (
            <Button
              size="sm"
              variant="outline"
              disabled={workspaceConfigBusy}
              onClick={() => void handleLockLocalVault().catch(() => {})}
            >
              <LockKeyhole data-icon="inline-start" />
              Lock
            </Button>
          ) : null
        }
      >
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-sm text-muted-foreground">
            {vault.message} The encrypted file lives outside this Git repository.
          </p>
          {vault.state === "unlocked" ? (
            <div className="flex shrink-0 items-center gap-2">
              <span className="text-xs text-muted-foreground">
                {vault.secret_count} secret{vault.secret_count === 1 ? "" : "s"}
              </span>
              <Button
                size="sm"
                variant="ghost"
                disabled={workspaceConfigBusy}
                onClick={() => setDialogMode("change")}
              >
                Change passphrase
              </Button>
            </div>
          ) : null}
        </div>
      </SettingsCard>

      <Dialog open={dialogMode !== null} onOpenChange={(open) => !open && closeDialog()}>
        <DialogContent>
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              void submit();
            }}
          >
            <DialogHeader>
              <DialogTitle>
                {dialogMode === "initialize"
                  ? "Set up encrypted vault"
                  : dialogMode === "change"
                    ? "Change vault passphrase"
                    : "Unlock encrypted vault"}
              </DialogTitle>
              <DialogDescription>
                {dialogMode === "unlock"
                  ? "The passphrase stays in this Renart process until you lock the vault or stop Renart."
                  : "Use at least 12 characters. Renart cannot recover a forgotten passphrase."}
              </DialogDescription>
            </DialogHeader>
            <FieldGroup>
              <Field>
                <Label htmlFor="local-vault-passphrase">
                  {dialogMode === "change" ? "New passphrase" : "Passphrase"}
                </Label>
                <Input
                  id="local-vault-passphrase"
                  type="password"
                  autoComplete={dialogMode === "unlock" ? "current-password" : "new-password"}
                  value={passphrase}
                  onChange={(event) => setPassphrase(event.target.value)}
                  autoFocus
                />
              </Field>
              {needsConfirmation ? (
                <Field>
                  <Label htmlFor="local-vault-passphrase-confirmation">Confirm passphrase</Label>
                  <Input
                    id="local-vault-passphrase-confirmation"
                    type="password"
                    autoComplete="new-password"
                    value={confirmation}
                    onChange={(event) => setConfirmation(event.target.value)}
                  />
                </Field>
              ) : null}
            </FieldGroup>
            {needsConfirmation && passphrase && Array.from(passphrase).length < 12 ? (
              <p className="text-xs text-destructive">Use at least 12 characters.</p>
            ) : null}
            {needsConfirmation && confirmation && passphrase !== confirmation ? (
              <p className="text-xs text-destructive">The passphrases do not match.</p>
            ) : null}
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={closeDialog}>
                Cancel
              </Button>
              <Button type="submit" disabled={!passphraseValid || workspaceConfigBusy}>
                {workspaceConfigBusy ? <LoaderCircle className="animate-spin" /> : null}
                {dialogMode === "initialize"
                  ? "Set up vault"
                  : dialogMode === "change"
                    ? "Change passphrase"
                    : "Unlock"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}

function ConnectionRow({
  connection,
  onSelect,
}: {
  connection: WorkspaceConfigConnection;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      className="group flex items-center gap-3 border-b px-3 py-2.5 text-left last:border-b-0 hover:bg-muted/50"
      onClick={onSelect}
    >
      <span className="min-w-0 flex-1 truncate font-mono text-sm font-medium">
        {connection.name}
      </span>
      <IntegrationBadge name={connection.type} />
      {connection.load_category ? (
        <Badge variant="secondary">{connection.load_category}</Badge>
      ) : null}
      <Pencil className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
    </button>
  );
}

function ConnectionSheet({
  state,
  onClose,
  settings,
}: {
  state: ConnectionSheetState | null;
  onClose: () => void;
  settings: ReturnType<typeof useWorkspaceSettingsData>;
}) {
  const {
    handleCreateWorkspaceConnection,
    handleDeleteWorkspaceConnection,
    handleUpdateWorkspaceConnection,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigBusy,
    workspaceConfigStatusMessage,
    workspaceConfigStatusTone,
  } = settings;
  const mode = state?.mode ?? "edit";
  const ingestrEnabled = useIngestrEnabled(workspaceConfig);
  // Stable identity matters: this array is an effect dependency inside
  // useWorkspaceConnectionForm, and a fresh [] per render loops the effect.
  // Ingestr/SaaS source types are hidden unless the feature is on, but the
  // type of the connection being edited always stays selectable.
  const editedConnectionType =
    state?.mode === "edit"
      ? workspaceConfig?.environments
          ?.find((environment) => environment.name === state.environment)
          ?.connections.find((connection) => connection.name === state.connection)?.type
      : undefined;
  const connectionTypes = useMemo(() => {
    const all = workspaceConfig?.connection_types ?? [];
    const visible = visibleConnectionTypes(all, ingestrEnabled);
    if (editedConnectionType && !visible.some((type) => type.type_name === editedConnectionType)) {
      const edited = all.find((type) => type.type_name === editedConnectionType);
      if (edited) {
        return [...visible, edited];
      }
    }
    return visible;
  }, [editedConnectionType, ingestrEnabled, workspaceConfig?.connection_types]);
  const [validateBusy, setValidateBusy] = useState(false);
  const [validateMessage, setValidateMessage] = useState<string | null>(null);
  const [validateTone, setValidateTone] = useState<"error" | "success" | null>(null);

  useEffect(() => {
    setValidateMessage(null);
    setValidateTone(null);
  }, [state]);

  const form = useWorkspaceConnectionForm({
    connectionTypes: connectionTypes,
    defaultEnvironment: workspaceConfig?.default_environment,
    environments: normalizedConfigEnvironments,
    mode,
    onCreateConnection: handleCreateWorkspaceConnection,
    onDeleteConnection: handleDeleteWorkspaceConnection,
    onModeChange: () => {},
    onSelectedConnectionChange: () => {},
    onSelectedEnvironmentChange: () => {},
    onUpdateConnection: handleUpdateWorkspaceConnection,
    selectedConnectionName: state?.mode === "edit" ? state.connection : null,
    selectedEnvironmentName: state?.environment ?? null,
  });

  const validateConnection = async () => {
    setValidateBusy(true);
    setValidateMessage(null);
    setValidateTone(null);
    try {
      const response = await testWorkspaceConnection({
        environment_name: form.connectionForm.environmentName,
        current_name: form.activeConnection?.name,
        name: form.connectionForm.name,
        type: form.connectionForm.type,
        values: form.connectionForm.values,
        secret_changes: form.connectionForm.secretChanges,
      });
      setValidateMessage(response.message ?? "Connection validated.");
      setValidateTone("success");
    } catch (error) {
      setValidateMessage(error instanceof Error ? error.message : "Connection validation failed.");
      setValidateTone("error");
    } finally {
      setValidateBusy(false);
    }
  };

  const save = async () => {
    try {
      await form.handleSave();
      onClose();
    } catch {
      // Keep the sheet open; the error alert below shows what failed.
    }
  };

  const remove = async () => {
    try {
      await form.handleDelete();
      onClose();
    } catch {
      // Keep the sheet open; the error alert below shows what failed.
    }
  };

  return (
    <Sheet open={state !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="min-h-0 overflow-hidden data-[side=right]:w-full data-[side=right]:max-w-full data-[side=right]:sm:max-w-xl">
        <SheetHeader className="shrink-0 p-4 sm:p-6">
          <SheetTitle className="flex items-center gap-2">
            <Plug className="size-4 text-primary" />
            {mode === "create" ? "New connection" : (form.activeConnection?.name ?? "Connection")}
          </SheetTitle>
          <SheetDescription>
            {mode === "create"
              ? "Sensitive values are write-only and scoped to this environment."
              : `Leave sensitive fields blank to keep their current value in ${state?.environment ?? ""}.`}
          </SheetDescription>
        </SheetHeader>
        <ScrollArea
          className="min-h-0 flex-1"
          viewportClassName="px-4 pb-4 [&>div]:!block [&>div]:w-full"
        >
          <div className="grid gap-4">
            {workspaceConfigStatusTone === "error" ? (
              <SettingsStatus
                message={workspaceConfigStatusMessage}
                tone={workspaceConfigStatusTone}
              />
            ) : null}
            <SecretBindingsAlert message={workspaceConfig?.secret_bindings_error} />
            <WorkspaceConnectionFormFields
              busy={workspaceConfigBusy}
              canValidate={Boolean(
                form.connectionForm.environmentName &&
                form.connectionForm.name.trim() &&
                form.connectionForm.type &&
                form.secretFieldsReady,
              )}
              connectionForm={form.connectionForm}
              connectionTypes={connectionTypes}
              environments={normalizedConfigEnvironments}
              mode={mode}
              selectedConnectionType={form.selectedConnectionType}
              localVaultState={workspaceConfig?.secret_vault.state}
              secretFields={form.activeConnection?.secret_fields}
              selectedEnvironment={state?.environment ?? null}
              environmentDisabled={mode === "edit"}
              typeDisabled={mode === "edit"}
              validateBusy={validateBusy}
              validateMessage={validateMessage}
              validateTone={validateTone}
              showActions={false}
              onEnvironmentChange={(value) =>
                form.setConnectionForm((current) => ({ ...current, environmentName: value }))
              }
              onFieldValueChange={(fieldName, value) =>
                form.setConnectionForm((current) => ({
                  ...current,
                  values: { ...current.values, [fieldName]: value },
                }))
              }
              onNameChange={(value) =>
                form.setConnectionForm((current) => ({ ...current, name: value }))
              }
              onSecretChange={(fieldName, change) =>
                form.setConnectionForm((current) => ({
                  ...current,
                  secretChanges: { ...current.secretChanges, [fieldName]: change },
                }))
              }
              onSave={() => void save()}
              onTypeChange={(value) =>
                form.setConnectionForm((current) => ({
                  ...current,
                  type: value,
                  values: buildConnectionFieldDefaults({
                    connectionTypes: connectionTypes,
                    existingConnection: null,
                    previousValues: current.values,
                    typeName: value,
                  }),
                  secretChanges: buildConnectionSecretChanges(
                    connectionTypes.find((connectionType) => connectionType.type_name === value),
                  ),
                }))
              }
              onValidate={() => void validateConnection()}
            />
          </div>
        </ScrollArea>
        <SheetFooter className="shrink-0 p-4 sm:p-6">
          <div className="flex w-full flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              {mode === "edit" && form.activeConnection ? (
                <ConfirmDeleteButton
                  disabled={workspaceConfigBusy}
                  label="Delete"
                  onConfirm={() => void remove()}
                />
              ) : null}
            </div>
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={
                  workspaceConfigBusy ||
                  validateBusy ||
                  !form.connectionForm.name.trim() ||
                  !form.secretFieldsReady
                }
                onClick={() => void validateConnection()}
              >
                {validateBusy ? (
                  <LoaderCircle data-icon="inline-start" className="animate-spin" />
                ) : (
                  <CheckCircle2 data-icon="inline-start" />
                )}
                Verify
              </Button>
              <Button
                size="sm"
                disabled={
                  workspaceConfigBusy ||
                  !form.connectionForm.environmentName ||
                  !form.connectionForm.name.trim() ||
                  !form.connectionForm.type ||
                  !form.secretFieldsReady
                }
                onClick={() => void save()}
              >
                {mode === "create" ? "Create connection" : "Save changes"}
              </Button>
            </div>
          </div>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

function SettingsCard({
  title,
  description,
  action,
  children,
}: {
  title: ReactNode;
  description?: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <DelimitedCard>
      <DelimitedCardHeader>
        <div className="min-w-0 flex-1">
          <DelimitedCardTitle>{title}</DelimitedCardTitle>
          {description ? <DelimitedCardDescription>{description}</DelimitedCardDescription> : null}
        </div>
        {action ? <DelimitedCardAction>{action}</DelimitedCardAction> : null}
      </DelimitedCardHeader>
      <DelimitedCardContent>{children}</DelimitedCardContent>
    </DelimitedCard>
  );
}

function SettingsStatus({
  message,
  tone,
}: {
  message?: string | null;
  tone?: "error" | "success" | null;
}) {
  if (!message || !tone) return null;
  return (
    <Alert variant={tone === "error" ? "destructive" : "default"}>
      <AlertTitle>{tone === "error" ? "Settings update failed" : "Settings saved"}</AlertTitle>
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
}

function SecretBindingsAlert({ message }: { message?: string }) {
  if (!message) {
    return null;
  }
  return (
    <Alert variant="destructive">
      <AlertTitle>Secret bindings need attention</AlertTitle>
      <AlertDescription className="whitespace-pre-wrap">{message}</AlertDescription>
    </Alert>
  );
}

function ConfirmDeleteButton({
  disabled,
  label,
  onConfirm,
}: {
  disabled: boolean;
  label: string;
  onConfirm: () => void;
}) {
  const [armed, setArmed] = useState(false);
  return (
    <Button
      size="sm"
      variant={armed ? "destructive" : "outline"}
      disabled={disabled}
      onBlur={() => setArmed(false)}
      onClick={() => {
        if (armed) {
          setArmed(false);
          onConfirm();
          return;
        }
        setArmed(true);
      }}
    >
      <Trash2 data-icon="inline-start" />
      {armed ? "Confirm delete" : label}
    </Button>
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

function PlainFieldGroup({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("grid gap-4", className)} {...props} />;
}

function PlainField({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("grid gap-2", className)} {...props} />;
}

function EnvironmentRow({
  environment,
  defaultEnvironment,
  policy,
  onSelect,
}: {
  environment: WorkspaceConfigEnvironment;
  defaultEnvironment?: string;
  policy?: EnvironmentPolicy;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      className="group flex items-center gap-3 border-b px-3 py-3 text-left last:border-b-0 hover:bg-muted/50"
      onClick={onSelect}
    >
      <Boxes className="size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate font-mono font-medium">{environment.name}</span>
          {environment.name === defaultEnvironment ? (
            <Badge variant="secondary">Default</Badge>
          ) : null}
          {policy?.protected ? (
            <Badge variant="outline" className="text-destructive">
              Protected
            </Badge>
          ) : null}
        </div>
        <div className="mt-1 truncate text-xs text-muted-foreground">
          {environment.schema_prefix
            ? `Schema prefix: ${environment.schema_prefix}`
            : "No schema prefix"}
        </div>
      </div>
      <span className="shrink-0 text-xs text-muted-foreground">
        {environment.connections.length} connection{environment.connections.length === 1 ? "" : "s"}
      </span>
      <Pencil className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
    </button>
  );
}

function EnvironmentPolicyFields({
  policy,
  disabled,
  onChange,
}: {
  policy: EnvironmentPolicy;
  disabled: boolean;
  onChange: (policy: EnvironmentPolicy) => void;
}) {
  return (
    <FieldGroup>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>Protected</FieldTitle>
          <FieldDescription>Disable interactive execution for this environment.</FieldDescription>
        </FieldContent>
        <Switch
          disabled={disabled}
          checked={policy.protected}
          onCheckedChange={(checked) => onChange({ ...policy, protected: checked })}
        />
      </Field>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>Deployed only</FieldTitle>
          <FieldDescription>Only run deployed snapshots for this environment.</FieldDescription>
        </FieldContent>
        <Switch
          disabled={disabled}
          checked={policy.deployed_only}
          onCheckedChange={(checked) => onChange({ ...policy, deployed_only: checked })}
        />
      </Field>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>Confirm destructive operations</FieldTitle>
          <FieldDescription>
            Require typing the environment name before destructive runs.
          </FieldDescription>
        </FieldContent>
        <Switch
          disabled={disabled}
          checked={policy.confirm_destructive}
          onCheckedChange={(checked) => onChange({ ...policy, confirm_destructive: checked })}
        />
      </Field>
    </FieldGroup>
  );
}
