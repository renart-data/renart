"use client";

import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import {
  Boxes,
  Cable,
  ChevronRight,
  Database,
  PencilLine,
  Plus,
  Star,
} from "lucide-react";

import { useWorkspaceSettingsLayout } from "@/components/workspace-settings-layout";
import { WorkspaceSettingsPageLayout } from "@/components/workspace-settings-page-layout";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { isDefaultEnvironment } from "@/lib/workspace-settings";

export function WorkspaceEnvironmentsHub() {
  const navigate = useNavigate();
  const { environmentId: selectedEnvironmentId } = useRouterState({
    select: (state) => {
      const params = state.matches[state.matches.length - 1]?.params as
        | { environmentId?: string }
        | undefined;

      return {
        environmentId: params?.environmentId ?? null,
      };
    },
  });
  const {
    handleUpdateWorkspaceEnvironment,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigBusy,
    workspaceConfigLoading,
  } = useWorkspaceSettingsLayout();

  return (
    <WorkspaceSettingsPageLayout
      title="Environments"
      description="Each environment has its own set of connections and configuration."
      actions={
        <Button asChild type="button" data-testid="environments-new-button">
          <Link to="/settings/environments/new">
            <Plus className="mr-2 size-4" />
            New Environment
          </Link>
        </Button>
      }
    >
      <div className="space-y-3">
        {workspaceConfigLoading ? (
          Array.from({ length: 3 }, (_, index) => (
            <Card key={index}>
              <CardContent className="px-5 py-5">
                <div className="flex items-center gap-3">
                  <Skeleton className="size-5" />
                  <div className="flex-1 space-y-2">
                    <Skeleton className="h-4 w-36" />
                    <Skeleton className="h-3 w-52" />
                  </div>
                </div>
              </CardContent>
            </Card>
          ))
        ) : normalizedConfigEnvironments.length > 0 ? (
          normalizedConfigEnvironments.map((environment) => {
            const expanded = environment.name === selectedEnvironmentId;

            return (
              <Card
                key={environment.name}
                className="overflow-hidden"
                data-testid={
                  environment.name === "default" ? "environment-card-default" : undefined
                }
              >
                <CardHeader className="bg-muted/30 px-4 py-3 sm:px-5">
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <button
                      type="button"
                      className="flex min-w-0 flex-1 items-start gap-3 text-left"
                      onClick={() => {
                        void navigate({
                          to: expanded
                            ? "/settings/environments"
                            : "/settings/environments/$environmentId",
                          params: expanded
                            ? undefined
                            : { environmentId: environment.name },
                        });
                      }}
                    >
                      <ChevronRight
                        className={`mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform ${
                          expanded ? "rotate-90" : "rotate-0"
                        }`}
                      />
                      <Boxes className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="truncate text-sm font-semibold text-foreground">
                            {environment.name}
                          </span>
                          {isDefaultEnvironment(workspaceConfig, environment) ? (
                            <span className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[10px] uppercase tracking-wide text-amber-700 dark:text-amber-300">
                              default
                            </span>
                          ) : null}
                        </div>
                        <p className="mt-1 text-xs text-muted-foreground">
                          {environment.connections.length} connection
                          {environment.connections.length === 1 ? "" : "s"}
                          {environment.schema_prefix
                            ? ` · schema prefix: ${environment.schema_prefix}`
                            : ""}
                        </p>
                      </div>
                    </button>

                    <div className="flex shrink-0 flex-wrap items-center gap-2">
                      {!isDefaultEnvironment(workspaceConfig, environment) ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          disabled={workspaceConfigBusy}
                          onClick={() => {
                            void handleUpdateWorkspaceEnvironment({
                              name: environment.name,
                              new_name: environment.name,
                              schema_prefix: environment.schema_prefix,
                              set_as_default: true,
                            });
                          }}
                        >
                          <Star className="mr-2 size-3.5" />
                          Set Default
                        </Button>
                      ) : null}

                      <Button asChild type="button" variant="ghost" size="sm">
                        <Link
                          to="/settings/environments/$environmentId/edit"
                          params={{ environmentId: environment.name }}
                        >
                          <PencilLine className="mr-2 size-3.5" />
                          Edit
                        </Link>
                      </Button>
                    </div>
                  </div>
                </CardHeader>

                {expanded ? (
                  <CardContent className="border-t px-0 py-0">
                    {environment.connections.length > 0 ? (
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead className="px-4 sm:px-5">Connection</TableHead>
                            <TableHead>Type</TableHead>
                            <TableHead className="w-px px-4 sm:px-5 text-right">
                              <Button asChild type="button" variant="outline" size="sm">
                                <Link
                                  to="/settings/environments/$environmentId/connections/new"
                                  params={{ environmentId: environment.name }}
                                  search={{ type: undefined }}
                                >
                                  <Plus className="mr-2 size-3.5" />
                                  Add Connection
                                </Link>
                              </Button>
                            </TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {environment.connections.map((connection) => (
                            <TableRow
                              key={connection.name}
                              className="cursor-pointer"
                              onClick={() => {
                                void navigate({
                                  to: "/settings/environments/$environmentId/connections/$connectionId",
                                  params: {
                                    environmentId: environment.name,
                                    connectionId: connection.name,
                                  },
                                });
                              }}
                            >
                              <TableCell className="px-4 sm:px-5">
                                <div className="flex items-center gap-3">
                                  <div className="rounded-md border bg-muted/30 p-1.5 text-muted-foreground">
                                    <Database className="size-4" />
                                  </div>
                                  <span className="font-medium text-foreground">
                                    {connection.name}
                                  </span>
                                </div>
                              </TableCell>
                              <TableCell>
                                <span className="rounded-full border bg-muted/20 px-2 py-0.5 text-xs text-muted-foreground">
                                  {connection.type}
                                </span>
                              </TableCell>
                              <TableCell
                                className="px-4 sm:px-5 text-right"
                                onClick={(event) => event.stopPropagation()}
                              >
                                <Button asChild type="button" variant="ghost" size="sm">
                                  <Link
                                    to="/settings/environments/$environmentId/connections/$connectionId"
                                    params={{
                                      environmentId: environment.name,
                                      connectionId: connection.name,
                                    }}
                                  >
                                    <Cable className="mr-2 size-3.5" />
                                    View
                                  </Link>
                                </Button>
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    ) : (
                      <div className="flex flex-col items-center justify-center gap-3 px-6 py-10 text-center">
                        <p className="text-sm text-muted-foreground">
                          No connections yet for {environment.name}.
                        </p>
                        <Button asChild type="button" size="sm">
                          <Link
                            to="/settings/environments/$environmentId/connections/new"
                            params={{ environmentId: environment.name }}
                            search={{ type: undefined }}
                          >
                            <Plus className="mr-2 size-3.5" />
                            Add Connection
                          </Link>
                        </Button>
                      </div>
                    )}
                  </CardContent>
                ) : null}
              </Card>
            );
          })
        ) : (
          <Card>
            <CardContent className="flex flex-col items-center justify-center gap-3 px-6 py-12 text-center">
              <p className="text-sm text-muted-foreground">
                No environments are configured yet.
              </p>
              <Button asChild type="button" size="sm">
                <Link to="/settings/environments/new">
                  <Plus className="mr-2 size-3.5" />
                  Create Environment
                </Link>
              </Button>
            </CardContent>
          </Card>
        )}
      </div>
    </WorkspaceSettingsPageLayout>
  );
}
