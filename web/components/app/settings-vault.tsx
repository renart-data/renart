import { KeyRound, LoaderCircle, LockKeyhole, UnlockKeyhole } from "lucide-react";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldGroup } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { SettingsCard } from "./settings-form-parts";

type LocalVaultDialogMode = "initialize" | "unlock" | "change";

export function LocalVaultCard({
  settings,
}: {
  settings: ReturnType<typeof useWorkspaceSettingsData>;
}) {
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
