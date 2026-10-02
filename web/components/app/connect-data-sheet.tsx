import { useNavigate } from "@tanstack/react-router";
import { useAtom, useAtomValue, useSetAtom } from "jotai";
import { LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { getWorkspace } from "@/lib/api-workspace";
import { connectDataHandoffAtom, openConnectDataAtom } from "@/lib/atoms/domains/connect-data";
import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/domains/workspace";

import { ConnectDataFlow, type ConnectDataResult } from "./connect-data-flow";

// "Connect your data" inside a project: the welcome screen's connect flow in a
// sheet, opened from Getting started, Connections and the Data browser.
export function ConnectDataSheet() {
  const [open, setOpen] = useAtom(openConnectDataAtom);
  const setHandoff = useSetAtom(connectDataHandoffAtom);
  const { workspaceConfig, loadWorkspaceConfig } = useWorkspaceSettingsData();
  const workspace = useAtomValue(workspaceAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const navigate = useNavigate();
  // Every opening starts a fresh flow.
  const [session, setSession] = useState(0);

  useEffect(() => {
    if (open) setSession((value) => value + 1);
  }, [open]);

  const environmentName =
    selectedEnvironment ||
    workspaceConfig?.selected_environment ||
    workspaceConfig?.default_environment ||
    "default";
  const pipelineNames = (workspace?.pipelines ?? []).map((pipeline) => pipeline.path);

  const handleImported = async (result: ConnectDataResult) => {
    setOpen(false);
    void loadWorkspaceConfig();
    let pipelineId: string | undefined;
    try {
      const latest = await getWorkspace();
      pipelineId = latest.pipelines.find(
        (pipeline) =>
          pipeline.path === result.pipelinePath || pipeline.name === result.pipelinePath,
      )?.id;
    } catch {
      // Fall back to the pipeline list below.
    }
    if (!pipelineId) {
      void navigate({ to: "/" });
      return;
    }
    setHandoff({ pipelineId, connectionName: result.connectionName, tables: result.tables });
    void navigate({ to: "/pipelines/$pipelineId/canvas", params: { pipelineId } });
  };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetContent side="right" className="w-full gap-0 overflow-y-auto sm:max-w-xl">
        <SheetHeader className="border-b">
          <SheetTitle>Connect your data</SheetTitle>
          <SheetDescription>
            Connect a database or warehouse, then import its tables as source assets. Renart tests
            the connection before saving it to the project config.
          </SheetDescription>
        </SheetHeader>
        <div className="grid gap-4 p-4">
          {workspaceConfig ? (
            <ConnectDataFlow
              key={session}
              config={workspaceConfig}
              environmentName={environmentName}
              defaultPipelineName="analytics"
              pipelineNames={pipelineNames}
              onCancel={() => setOpen(false)}
              cancelLabel="Cancel"
              onImported={(result) => void handleImported(result)}
            />
          ) : (
            <div className="flex justify-center py-10">
              <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
