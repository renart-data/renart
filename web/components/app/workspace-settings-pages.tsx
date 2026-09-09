import { useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { SettingsNavigator } from "./settings-navigator";
import type { SettingsSearch } from "@/lib/settings-navigation";
import {
  Empty,
  EmptyHeader,
  EmptyTitle,
  EmptyDescription,
  EmptyContent,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { useResourceNavigation } from "@/hooks/use-resource-navigation";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { useWorkbench } from "./workbench/workbench-slots";
import { SettingsStatus, SecretBindingsAlert } from "./settings-form-parts";
import { EnvironmentEditor, type EnvironmentEditorState } from "./settings-environment-editor";
import { ConnectionEditor, type ConnectionEditorState } from "./settings-connection-editor";
import { LocalVaultCard } from "./settings-vault";

export function AppProjectEnvironmentsPage({ environment, action }: SettingsSearch) {
  const settings = useWorkspaceSettingsData();
  const navigate = useNavigate();
  const [revision, setRevision] = useState(0);
  const policy = useSettingsPolicies(settings);
  const state: EnvironmentEditorState | null =
    action === "create"
      ? { mode: "create" }
      : environment
        ? { mode: action === "clone" ? "clone" : "edit", name: environment }
        : null;
  const select = (next: EnvironmentEditorState | null, replace = false) =>
    void navigate({
      to: "/project/environments",
      replace,
      search: (search) => ({
        ...search,
        detail: undefined,
        connection: undefined,
        environment: next && next.mode !== "create" ? next.name : undefined,
        action: next?.mode === "edit" ? undefined : next?.mode,
      }),
    });
  const ready =
    state?.mode !== "edit" ||
    Boolean(environment && settings.workspaceEnvironmentPolicies[environment]);
  const exists =
    !environment || settings.normalizedConfigEnvironments.some((item) => item.name === environment);
  return (
    <div className="flex min-h-0 flex-col gap-4">
      <SettingsNavigator
        key={settings.workspaceConfig?.project_id ?? "loading"}
        section="environments"
        settings={settings}
        environment={environment}
      />
      <SettingsLoadStatus settings={settings} />
      {policy.error ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load environment policies</AlertTitle>
          <AlertDescription>
            {policy.error}
            <Button variant="outline" size="sm" onClick={policy.retry}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}
      {settings.workspaceConfig && state ? (
        !exists ? (
          <Alert variant="destructive">
            <AlertTitle>Environment not found</AlertTitle>
            <AlertDescription>
              The linked environment is no longer available. Select another environment from the
              navigator.
            </AlertDescription>
          </Alert>
        ) : ready ? (
          <EnvironmentEditor
            key={JSON.stringify([state, revision])}
            state={state}
            onStateChange={select}
            settings={settings}
            onSaved={(name) => {
              setRevision((value) => value + 1);
              select({ mode: "edit", name }, true);
            }}
          />
        ) : (
          <Skeleton className="h-48 w-full" />
        )
      ) : settings.workspaceConfig ? (
        <SettingsOverview section="environments" onCreate={() => select({ mode: "create" })} />
      ) : null}
    </div>
  );
}

export function AppProjectConnectionsPage({
  selectedEnvironment,
  selectedConnection,
  action,
}: {
  selectedEnvironment?: string;
  selectedConnection?: string;
  action?: SettingsSearch["action"];
}) {
  const navigate = useNavigate();
  const settings = useWorkspaceSettingsData();
  const resource = useResourceNavigation();
  const [revision, setRevision] = useState(0);
  const linked = resource.detail?.target.kind === "connection" ? resource.detail : undefined;
  selectedConnection = linked?.target.connection ?? selectedConnection;
  selectedEnvironment = linked?.environment ?? selectedEnvironment;
  const matches = settings.normalizedConfigEnvironments.flatMap((item) =>
    !selectedEnvironment || item.name === selectedEnvironment
      ? item.connections
          .filter((connection) => connection.name === selectedConnection)
          .map((connection) => ({ environment: item.name, connection: connection.name }))
      : [],
  );
  // A qualified identity remains mounted while a save renames it. The editor
  // validates the immutable initial snapshot instead of falling back to a sibling.
  const state: ConnectionEditorState | null = selectedConnection
    ? selectedEnvironment
      ? { mode: "edit", environment: selectedEnvironment, connection: selectedConnection }
      : matches.length === 1
        ? { mode: "edit", ...matches[0] }
        : null
    : action === "create"
      ? {
          mode: "create",
          environment: selectedEnvironment ?? settings.workspaceConfig?.default_environment ?? null,
        }
      : null;
  const close = () =>
    void navigate({
      to: "/project/connections",
      search: (search) => ({
        ...search,
        detail: undefined,
        connection: undefined,
        action: undefined,
      }),
      replace: true,
    });
  return (
    <div className="flex min-h-0 flex-col gap-4">
      <SettingsNavigator
        key={settings.workspaceConfig?.project_id ?? "loading"}
        section="connections"
        settings={settings}
        environment={selectedEnvironment}
        connection={selectedConnection}
      />
      <SettingsLoadStatus settings={settings} />
      <SecretBindingsAlert message={settings.workspaceConfig?.secret_bindings_error} />
      <SecretBindingsAlert
        title="Connection access needs attention"
        message={settings.workspaceConfig?.connection_policy_error}
      />
      {settings.workspaceConfig && state ? (
        <ConnectionEditor
          key={JSON.stringify([state, revision])}
          state={state}
          onClose={close}
          settings={settings}
          focusedField={linked?.target.field}
          onSaved={(environment, connection) => {
            setRevision((value) => value + 1);
            void navigate({
              to: "/project/connections",
              replace: true,
              search: (search) => ({
                ...search,
                detail: undefined,
                action: undefined,
                environment,
                connection,
              }),
            });
          }}
        />
      ) : settings.workspaceConfig ? (
        selectedConnection ? (
          <Alert variant="destructive">
            <AlertTitle>Connection not found</AlertTitle>
            <AlertDescription>
              The linked connection is missing or ambiguous. Select its environment and connection
              in the navigator.
            </AlertDescription>
          </Alert>
        ) : (
          <>
            {action !== "vault" ? (
              <SettingsOverview
                section="connections"
                onCreate={() =>
                  void navigate({
                    to: "/project/connections",
                    search: (search) => ({
                      ...search,
                      detail: undefined,
                      action: "create",
                      connection: undefined,
                    }),
                  })
                }
              />
            ) : null}
            {settings.workspaceConfig.secret_vault ? <LocalVaultCard settings={settings} /> : null}
          </>
        )
      ) : null}
    </div>
  );
}

function useSettingsPolicies(settings: ReturnType<typeof useWorkspaceSettingsData>) {
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const names = settings.normalizedConfigEnvironments
    .map((environment) => environment.name)
    .join("\0");
  const load = settings.loadWorkspaceEnvironmentPolicy;
  useEffect(() => {
    let cancelled = false;
    setError(null);
    void Promise.all(names ? names.split("\0").map(load) : []).catch((cause) => {
      if (!cancelled) setError(cause instanceof Error ? cause.message : "Could not load policies.");
    });
    return () => {
      cancelled = true;
    };
  }, [names, load, attempt]);
  return { error, retry: () => setAttempt((value) => value + 1) };
}

function SettingsLoadStatus({
  settings,
}: {
  settings: ReturnType<typeof useWorkspaceSettingsData>;
}) {
  return (
    <>
      <SettingsStatus
        message={settings.workspaceConfigStatusMessage}
        tone={settings.workspaceConfigStatusTone}
      />
      {!settings.workspaceConfig && settings.workspaceConfigLoading ? (
        <Skeleton className="h-36 w-full" />
      ) : null}
      {!settings.workspaceConfig && !settings.workspaceConfigLoading ? (
        <Button variant="outline" onClick={() => void settings.loadWorkspaceConfig()}>
          Retry loading settings
        </Button>
      ) : null}
    </>
  );
}

function SettingsOverview({
  section,
  onCreate,
}: {
  section: "connections" | "environments";
  onCreate: () => void;
}) {
  const { setMobileNavigationOpen, dispatch, navigation } = useWorkbench();
  return (
    <Empty className="min-h-56 border">
      <EmptyHeader>
        <EmptyTitle>{section === "connections" ? "Connections" : "Environments"}</EmptyTitle>
        <EmptyDescription>
          {section === "connections"
            ? "Choose a connection to edit its values, credentials and access."
            : "Choose an environment to edit its defaults and execution guardrails."}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button onClick={onCreate}>
          <Plus data-icon="inline-start" />
          {section === "connections" ? "New connection" : "New environment"}
        </Button>
        <Button
          variant="ghost"
          onClick={() => {
            if (navigation)
              dispatch({ type: "tool-selected", mode: navigation.mode, tool: section });
            setMobileNavigationOpen(true);
          }}
        >
          Browse {section}
        </Button>
      </EmptyContent>
    </Empty>
  );
}
