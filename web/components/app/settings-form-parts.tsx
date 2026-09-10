import { Check, Trash2 } from "lucide-react";
import { HTMLAttributes, ReactNode, useState } from "react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldGroup } from "@/components/ui/field";
import {
  DelimitedCard,
  DelimitedCardAction,
  DelimitedCardContent,
  DelimitedCardDescription,
  DelimitedCardHeader,
  DelimitedCardTitle,
} from "@/components/ui/delimited-card";

export function SettingsCard({
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

export function SettingsStatus({
  message,
  tone,
}: {
  message?: string | null;
  tone?: "error" | "success" | null;
}) {
  if (!message || !tone) return null;
  if (tone === "success")
    return (
      <p role="status" className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
        <Check className="size-3.5 shrink-0 text-primary" />
        {message}
      </p>
    );
  return (
    <Alert variant="destructive">
      <AlertTitle>Settings update failed</AlertTitle>
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
}

export function SecretBindingsAlert({
  message,
  title = "Secret bindings need attention",
}: {
  message?: string;
  title?: string;
}) {
  if (!message) {
    return null;
  }
  return (
    <Alert variant="destructive">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription className="whitespace-pre-wrap">{message}</AlertDescription>
    </Alert>
  );
}

export function ConfirmDeleteButton({
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

export function PlainFieldGroup({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <FieldGroup className={className} {...props} />;
}

export function PlainField({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <Field className={className} {...props} />;
}

export function SettingsEditorHeader({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-2">
      <h1 className="text-base font-semibold tracking-tight">{title}</h1>
      <p className="text-xs leading-relaxed text-muted-foreground">{description}</p>
      {children}
    </header>
  );
}
