import { Check } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

import type { WelcomeStep, WelcomeStepLabel } from "./welcome-flow";

// One stable frame for every step: the header and width never change, the
// step content fades in on the left and the live preview stays on the right.
export function WelcomeFrame({
  steps,
  currentStep,
  stepKey,
  children,
  preview,
  previewOnMobile = false,
}: {
  steps: WelcomeStepLabel[] | null;
  currentStep: WelcomeStep;
  // Changes whenever the left column should animate in again. Changing it
  // remounts the column, so stateful steps (the connect flow) keep one key.
  stepKey: string;
  children: ReactNode;
  preview: ReactNode;
  previewOnMobile?: boolean;
}) {
  return (
    <div className="flex min-h-dvh flex-col overflow-auto bg-muted/40 text-foreground">
      <div className="mx-auto flex w-full max-w-6xl min-w-0 flex-1 flex-col px-4 py-6 md:px-8 md:py-10">
        <header className="mb-6 flex min-w-0 flex-col gap-4 sm:flex-row sm:items-center sm:justify-between md:mb-8">
          <div className="flex min-w-0 items-center gap-3">
            <img src="/icons/icon.svg" alt="" aria-hidden className="size-10 shrink-0 rounded-xl" />
            <div className="min-w-0">
              <h1 className="text-xl font-semibold tracking-tight">Welcome to Renart</h1>
              <p className="text-sm text-muted-foreground sm:truncate">
                An open source data platform that works inside your Git repository.
              </p>
            </div>
          </div>
          {steps ? <StepIndicator steps={steps} currentStep={currentStep} /> : null}
        </header>

        <div className="grid min-w-0 flex-1 items-start gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)] lg:gap-8">
          <div
            key={stepKey}
            className="grid min-w-0 gap-4 motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-2 motion-safe:duration-200 motion-safe:ease-out"
          >
            {children}
          </div>
          <div
            className={cn(
              "min-w-0 lg:sticky lg:top-10",
              previewOnMobile ? "block" : "hidden lg:block",
            )}
          >
            {preview}
          </div>
        </div>
      </div>
    </div>
  );
}

function StepIndicator({
  steps,
  currentStep,
}: {
  steps: WelcomeStepLabel[];
  currentStep: WelcomeStep;
}) {
  const currentIndex = Math.max(
    0,
    steps.findIndex((step) => step.id === currentStep),
  );
  return (
    <ol aria-label="Setup progress" className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
      {steps.map((step, index) => {
        const done = index < currentIndex;
        const current = index === currentIndex;
        return (
          <li key={step.id} className="flex items-center gap-1.5">
            {index > 0 ? (
              <span
                aria-hidden
                className={cn(
                  "h-px w-4 transition-colors duration-200 sm:w-6",
                  done || current ? "bg-primary/60" : "bg-border",
                )}
              />
            ) : null}
            <span
              aria-current={current ? "step" : undefined}
              className={cn(
                "flex items-center gap-1.5 rounded-full border px-2 py-0.5 transition-colors duration-200",
                current
                  ? "border-primary/50 bg-primary/10 font-medium text-foreground"
                  : done
                    ? "border-transparent text-foreground"
                    : "border-transparent text-muted-foreground",
              )}
            >
              <span
                aria-hidden
                className={cn(
                  "flex size-4 items-center justify-center rounded-full text-[10px]",
                  done
                    ? "bg-primary text-primary-foreground"
                    : current
                      ? "bg-primary/20 text-primary"
                      : "bg-muted text-muted-foreground",
                )}
              >
                {done ? <Check className="size-2.5" /> : index + 1}
              </span>
              {step.label}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

// The right-hand pane: a titled card around a canvas or illustration.
export function WelcomePreviewPane({
  title,
  meta,
  children,
  footer,
  className,
}: {
  title: ReactNode;
  meta?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
}) {
  return (
    <section
      aria-label="Preview"
      className={cn("overflow-hidden rounded-xl border bg-background shadow-sm", className)}
    >
      <div className="flex min-w-0 items-center justify-between gap-3 border-b px-4 py-2.5">
        <div className="min-w-0 truncate text-sm font-medium">{title}</div>
        {meta ? <div className="shrink-0 text-xs text-muted-foreground">{meta}</div> : null}
      </div>
      {children}
      {footer ? <div className="border-t px-4 py-3">{footer}</div> : null}
    </section>
  );
}
