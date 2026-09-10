import { useSetAtom } from "jotai";
import { Plus } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
  DATA_BROWSER_MIME,
  dataBrowserTransferAtom,
  type DataBrowserTransfer,
  type DataBrowserDestination,
} from "@/lib/data-browser-transfer";
import { getPinnedProjectId } from "@/lib/project-context";

let transferSequence = 0;

export function DataBrowserTransferItem({
  children,
  item,
  destination,
  disabledReason,
  environment,
  onChoose,
}: {
  children: ReactNode;
  item?: Pick<DataBrowserTransfer, "kind" | "id" | "label" | "referenceText">;
  destination?: DataBrowserDestination;
  disabledReason?: string;
  environment: string;
  onChoose?: () => void;
}) {
  const setTransfer = useSetAtom(dataBrowserTransferAtom);
  const enabled = Boolean(destination && item && !disabledReason);
  const pickUp = (method: DataBrowserTransfer["method"]) => {
    if (!destination || !item || disabledReason) return null;
    const transfer: DataBrowserTransfer = {
      ...item,
      destination,
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
      title={disabledReason}
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
              if (transfer.kind === "table" || transfer.kind === "file") {
                // Links otherwise drag as a URL/text fragment. Capture the
                // actual object row as a compact themed card, like connections.
                const row = event.currentTarget.firstElementChild;
                if (row) {
                  const preview = row.cloneNode(true) as HTMLElement;
                  preview.className =
                    "pointer-events-none fixed z-50 w-64 rounded-lg border bg-card p-1 text-card-foreground shadow-lg";
                  preview.style.left = `${event.clientX + 12}px`;
                  preview.style.top = `${event.clientY + 12}px`;
                  preview.setAttribute("aria-hidden", "true");
                  preview.setAttribute("data-data-browser-drag-preview", "");
                  preview.inert = true;
                  document.body.appendChild(preview);
                  try {
                    event.dataTransfer.setDragImage(preview, 12, 20);
                  } finally {
                    requestAnimationFrame(() => preview.remove());
                  }
                }
              }
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
      {destination && item ? (
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 text-muted-foreground md:opacity-0 md:group-hover/transfer:opacity-100 md:focus-visible:opacity-100"
          disabled={!enabled}
          aria-label={
            destination.kind === "notebook"
              ? `Add ${item.label} to notebook`
              : `Use ${item.label} in canvas`
          }
          title={
            disabledReason ||
            (destination.kind === "notebook"
              ? "Add to notebook, or drag between blocks"
              : "Use in canvas, or drag onto it")
          }
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
