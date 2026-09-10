import { Link, useLocation } from "@tanstack/react-router";
import { Boxes, ChevronRight, CircleAlert, KeyRound, LockKeyhole, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Separator } from "@/components/ui/separator";
import { ConnectionTypeIcon } from "./connection-type-icon";
import type { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { filterSettingsEnvironments, settingsEditorIdentity } from "@/lib/settings-navigation";
import { cn } from "@/lib/utils";
import { ResourceLink } from "./resource-link";
import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";

type NavigatorState = { filter: string; collapsed: string[] };
// Disposable navigation preferences only. Drafts and secrets never enter storage.
const navigationPreferences = new Map<string, NavigatorState>();

export function SettingsNavigator({
  section,
  settings,
  environment,
  connection,
}: {
  section: "connections" | "environments";
  settings: ReturnType<typeof useWorkspaceSettingsData>;
  environment?: string;
  connection?: string;
}) {
  const { workspaceConfig, normalizedConfigEnvironments, workspaceEnvironmentPolicies } = settings;
  const { navigation, setMobileNavigationOpen } = useWorkbench();
  const location = useLocation();
  const identity = settingsEditorIdentity(location, workspaceConfig?.project_id);
  // Closing after a committed route transition keeps the sheet open on Stay.
  useEffect(() => {
    setMobileNavigationOpen(false);
  }, [identity, setMobileNavigationOpen]);
  const preferenceKey = `${workspaceConfig?.project_id ?? workspaceConfig?.workspace_path ?? ""}/${section}`;
  const [preferences, setPreferences] = useState<NavigatorState>(
    () => navigationPreferences.get(preferenceKey) ?? { filter: "", collapsed: [] },
  );
  useEffect(() => {
    navigationPreferences.set(preferenceKey, preferences);
    if (navigationPreferences.size > 32)
      navigationPreferences.delete(navigationPreferences.keys().next().value!);
  }, [preferenceKey, preferences]);
  const environments = filterSettingsEnvironments(
    normalizedConfigEnvironments,
    preferences.filter,
    { environment, connection },
  );
  const rows = (
    <AppContextSidebarFrame
      title={section === "connections" ? "Connections" : "Environments"}
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={section === "connections" ? "New connection" : "New environment"}
          asChild
        >
          <Link
            to={section === "connections" ? "/project/connections" : "/project/environments"}
            search={(search) => ({
              ...search,
              detail: undefined,
              connection: undefined,
              environment: section === "connections" ? environment : undefined,
              action: "create",
            })}
          >
            <Plus />
          </Link>
        </Button>
      }
    >
      <div className="flex flex-col gap-3 p-2" data-testid="settings-navigator">
        <Input
          aria-label={`Filter ${section}`}
          placeholder={`Filter ${section}…`}
          value={preferences.filter}
          onChange={(event) =>
            setPreferences((current) => ({ ...current, filter: event.target.value }))
          }
        />
        {!workspaceConfig && settings.workspaceConfigLoading ? (
          <div className="grid gap-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-4/5" />
          </div>
        ) : null}
        <nav
          aria-label={section === "connections" ? "Project connections" : "Project environments"}
          className="flex flex-col gap-1"
        >
          {environments.map((item) =>
            section === "environments" ? (
              <Link
                key={item.name}
                to="/project/environments"
                search={(search) => ({
                  ...search,
                  environment: item.name,
                  connection: undefined,
                  action: undefined,
                  detail: undefined,
                })}
                aria-current={environment === item.name ? "page" : undefined}
                className={cn(
                  "flex min-h-8 min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-muted",
                  environment === item.name && "bg-accent text-accent-foreground",
                )}
              >
                <Boxes className="size-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate">{item.name}</span>
                {item.name === workspaceConfig?.default_environment ? (
                  <Badge variant="secondary">Default</Badge>
                ) : null}
                {workspaceEnvironmentPolicies[item.name]?.protected ? (
                  <Badge variant="outline">Protected</Badge>
                ) : null}
              </Link>
            ) : (
              <Collapsible
                key={item.name}
                open={Boolean(preferences.filter) || !preferences.collapsed.includes(item.name)}
                onOpenChange={(open) =>
                  setPreferences((current) => ({
                    ...current,
                    collapsed: open
                      ? current.collapsed.filter((name) => name !== item.name)
                      : [...current.collapsed, item.name],
                  }))
                }
              >
                <div className="flex items-center gap-1">
                  <CollapsibleTrigger asChild>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="min-w-0 flex-1 justify-start"
                      aria-label={`Toggle ${item.name} connections`}
                    >
                      <ChevronRight
                        data-icon="inline-start"
                        className={cn(
                          (preferences.filter || !preferences.collapsed.includes(item.name)) &&
                            "rotate-90",
                        )}
                      />
                      <span className="truncate">{item.name}</span>
                      {item.name === workspaceConfig?.default_environment ? (
                        <Badge variant="secondary">Default</Badge>
                      ) : null}
                    </Button>
                  </CollapsibleTrigger>
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label={`New connection in ${item.name}`}
                    asChild
                  >
                    <Link
                      to="/project/connections"
                      search={(search) => ({
                        ...search,
                        environment: item.name,
                        connection: undefined,
                        detail: undefined,
                        action: "create",
                      })}
                    >
                      <Plus />
                    </Link>
                  </Button>
                </div>
                <CollapsibleContent className="ml-3 border-l pl-2">
                  {item.connections.map((candidate) => (
                    <ResourceLink
                      key={candidate.name}
                      target={{ kind: "connection", connection: candidate.name }}
                      environment={item.name}
                      className={cn(
                        "my-0.5 flex min-h-8 min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-muted",
                        item.name === environment &&
                          candidate.name === connection &&
                          "bg-accent text-accent-foreground",
                      )}
                    >
                      <ConnectionTypeIcon connectionType={candidate.type} className="size-5" />
                      <span className="min-w-0 flex-1 truncate">{candidate.name}</span>
                      {candidate.access_mode === "read_only" ? (
                        <LockKeyhole
                          role="img"
                          aria-label="Read-only"
                          className="size-3 shrink-0 text-muted-foreground"
                        />
                      ) : null}
                      {Object.values(candidate.secret_fields ?? {}).some(
                        (field) =>
                          ["permission_required", "unavailable", "error"].includes(field.status) ||
                          (field.status === "missing" && Boolean(field.reference)),
                      ) ? (
                        <CircleAlert
                          role="img"
                          aria-label="Credentials need attention"
                          className="size-3.5 shrink-0 text-destructive"
                        />
                      ) : null}
                    </ResourceLink>
                  ))}
                  {!item.connections.length ? (
                    <p className="px-2 py-2 text-xs text-muted-foreground">No connections</p>
                  ) : null}
                </CollapsibleContent>
              </Collapsible>
            ),
          )}
          {!environments.length && workspaceConfig ? (
            <p className="p-2 text-xs text-muted-foreground">
              {preferences.filter ? "No matches." : "No environments yet."}
            </p>
          ) : null}
        </nav>
        {section === "connections" ? (
          <>
            <Separator />
            <Button variant="ghost" size="sm" className="justify-start" asChild>
              <Link
                to="/project/connections"
                search={(search) => ({
                  ...search,
                  connection: undefined,
                  environment: undefined,
                  detail: undefined,
                  action: "vault",
                })}
              >
                <KeyRound data-icon="inline-start" />
                Encrypted vault
              </Link>
            </Button>
          </>
        ) : null}
      </div>
    </AppContextSidebarFrame>
  );
  return navigation?.workbench ? <WorkbenchPortal slot="context">{rows}</WorkbenchPortal> : rows;
}
