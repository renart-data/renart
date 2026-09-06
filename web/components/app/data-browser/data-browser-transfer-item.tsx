import { useSetAtom } from "jotai";
import { Plus } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
  DATA_BROWSER_MIME,
  dataBrowserTransferAtom,
  type DataBrowserTransfer,
} from "@/lib/data-browser-transfer";
import { getPinnedProjectId } from "@/lib/project-context";

let transferSequence = 0;

export function DataBrowserTransferItem({
  children,
  item,
  pipelineId,
  environment,
  onChoose,
}: {
  children: ReactNode;
  item?: Pick<DataBrowserTransfer, "kind" | "id" | "label">;
  pipelineId?: string;
  environment: string;
  onChoose?: () => void;
}) {
  const setTransfer = useSetAtom(dataBrowserTransferAtom);
  const enabled = Boolean(pipelineId && item);
  const pickUp = (method: DataBrowserTransfer["method"]) => {
    if (!pipelineId || !item) return null;
    const transfer: DataBrowserTransfer = {
      ...item,
      pipelineId,
      environment,
      projectId: getPinnedProjectId(),
      method,
      token: globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${++transferSequence}`,
    };
    setTransfer(transfer);
    return transfer;
  };
  return (
    <div
      className="group/transfer flex min-w-0 items-center gap-1"
      draggable={enabled}
      data-testid={enabled ? "data-browser-transfer-item" : undefined}
      data-transfer-label={enabled ? item?.label : undefined}
      onDragStart={
        enabled
          ? (event) => {
              const transfer = pickUp("drag");
              if (!transfer) return;
              event.dataTransfer.clearData();
              event.dataTransfer.effectAllowed = "copy";
              event.dataTransfer.setData(DATA_BROWSER_MIME, transfer.token);
            }
          : undefined
      }
      onDragEnd={
        enabled
          ? () => setTransfer((current) => (current?.method === "drag" ? null : current))
          : undefined
      }
    >
      <div className="min-w-0 flex-1">{children}</div>
      {enabled ? (
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 text-muted-foreground md:opacity-0 md:group-hover/transfer:opacity-100 md:focus-visible:opacity-100"
          aria-label={`Use ${item?.label} in canvas`}
          title="Use in canvas, or drag onto it"
          onClick={() => {
            pickUp("choose");
            onChoose?.();
          }}
        >
          <Plus />
        </Button>
      ) : null}
    </div>
  );
}
