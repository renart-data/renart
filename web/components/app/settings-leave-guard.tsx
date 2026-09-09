import { useBlocker } from "@tanstack/react-router";
import { useRef, useState } from "react";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { settingsEditorIdentity } from "@/lib/settings-navigation";

// One guard for both editors. Only a successful save may release a pending
// navigation; failed validation or writes keep both the route and draft intact.
export function useSettingsLeaveGuard({
  dirty,
  busy,
  canSave,
  save,
  project,
}: {
  dirty: boolean;
  busy: boolean;
  canSave: boolean;
  save: () => Promise<boolean>;
  project?: string;
}) {
  const accepting = useRef(false);
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const blocker = useBlocker({
    shouldBlockFn: ({ current, next }) =>
      !accepting.current &&
      (dirty || busy) &&
      settingsEditorIdentity(current, project) !== settingsEditorIdentity(next, project),
    enableBeforeUnload: () => !accepting.current && (dirty || busy),
    withResolver: true,
  });
  return {
    allowNavigation: () => {
      accepting.current = true;
    },
    protectNavigation: () => {
      accepting.current = false;
    },
    dialog: (
      <AlertDialog
        open={blocker.status === "blocked"}
        onOpenChange={(open) => {
          if (!open && !saving && !busy) {
            setFailed(false);
            blocker.reset?.();
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Unsaved changes</AlertDialogTitle>
            <AlertDialogDescription>
              {failed
                ? "Could not save. Stay here to review the error, or discard your changes."
                : "Save your changes before leaving this editor?"}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <Button
              variant="outline"
              disabled={saving || busy}
              onClick={() => {
                setFailed(false);
                blocker.reset?.();
              }}
            >
              Stay
            </Button>
            <Button
              variant="ghost"
              disabled={saving || busy}
              onClick={() => {
                accepting.current = true;
                blocker.proceed?.();
              }}
            >
              Discard
            </Button>
            <Button
              disabled={!canSave || saving || busy}
              onClick={async () => {
                setSaving(true);
                try {
                  if (await save()) {
                    accepting.current = true;
                    blocker.proceed?.();
                  } else setFailed(true);
                } finally {
                  setSaving(false);
                }
              }}
            >
              Save and continue
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    ),
  };
}
