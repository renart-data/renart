"use client";

import { useAtomValue, useSetAtom } from "jotai";
import {
  AtSign,
  ArrowUp,
  Bot,
  Check,
  CircleAlert,
  Database,
  FileCode,
  Plug,
  RotateCcw,
  ShieldCheck,
  Square,
  UserRound,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";

import { Bubble, BubbleContent } from "@/components/ui/bubble";
import { ConnectionSelect, type ConnectionSelectGroup } from "@/components/app/connection-select";
import { WorkspaceConnectionDialog } from "@/components/app/workspace-connection-dialog";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupTextarea,
} from "@/components/ui/input-group";
import { Marker, MarkerContent, MarkerIcon } from "@/components/ui/marker";
import {
  Message,
  MessageAvatar,
  MessageContent,
  MessageFooter,
  MessageHeader,
} from "@/components/ui/message";
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Questionnaire,
  QuestionnaireActions,
  QuestionnaireChoice,
  QuestionnaireChoiceDescription,
  QuestionnaireChoices,
  QuestionnaireDescription,
  QuestionnaireError,
  QuestionnaireInput,
  QuestionnaireItem,
  QuestionnaireNext,
  QuestionnairePrevious,
  QuestionnaireProgress,
  QuestionnaireSkip,
  QuestionnaireSubmit,
  QuestionnaireTitle,
} from "@/components/ui/questionnaire";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  answerNotebookAgentInteraction,
  cancelNotebookAgentTurn,
  getNotebookAgent,
  type NotebookAgentActivity,
  type NotebookAgentInteraction,
  type NotebookAgentMessage,
  type NotebookAgentMode,
  type NotebookAgentProvider,
  type NotebookAgentQuestionAnswer,
  type NotebookAgentReference,
  resetNotebookAgent,
  startNotebookAgentTurn,
} from "@/lib/api-notebooks";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { mergeNotebookAgentEvent, notebookAgentEventsAtom } from "@/lib/atoms/domains/results";
import { workspaceAtom, workspaceReconnectSequenceAtom } from "@/lib/atoms/domains/workspace";
import { friendlyConnectionType, normalizeConnectionType } from "./connection-type-icon";
import type { WorkspaceState } from "@/lib/types";
import { cn } from "@/lib/utils";

type NotebookAgentTurn = {
  user: NotebookAgentMessage;
  assistant?: NotebookAgentMessage;
  activities: NotebookAgentActivity[];
  interaction?: NotebookAgentInteraction;
};

type NotebookAgentReferenceCandidate = NotebookAgentReference & {
  group: "cells" | "assets";
};

const maxNotebookAgentReferences = 12;

export function NotebookAgentChat({
  notebookId,
  onClose,
}: {
  notebookId: string;
  onClose?: () => void;
}) {
  const conversation = useAtomValue(notebookAgentEventsAtom)[notebookId];
  const workspace = useAtomValue(workspaceAtom);
  const workspaceReconnectSequence = useAtomValue(workspaceReconnectSequenceAtom);
  const setAgentEvents = useSetAtom(notebookAgentEventsAtom);
  const [providers, setProviders] = useState<NotebookAgentProvider[]>([]);
  const [provider, setProvider] = useState<NotebookAgentProvider["id"]>("codex");
  const [mode, setMode] = useState<NotebookAgentMode>("ask");
  const [draft, setDraft] = useState("");
  const [references, setReferences] = useState<NotebookAgentReferenceCandidate[]>([]);
  const [referencePickerOpen, setReferencePickerOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [requestBusy, setRequestBusy] = useState(false);
  const [interactionBusy, setInteractionBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const savedProvider = window.localStorage.getItem("renart-notebook-agent-provider");
    if (savedProvider === "codex" || savedProvider === "claude" || savedProvider === "opencode") {
      setProvider(savedProvider);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    getNotebookAgent(notebookId)
      .then((state) => {
        if (cancelled) return;
        setProviders(state.providers);
        setAgentEvents((current) => mergeNotebookAgentEvent(current, state.conversation));
        setProvider((current) => {
          const currentAvailable = state.providers.some(
            (candidate) => candidate.id === current && candidate.available,
          );
          return currentAvailable
            ? current
            : (state.providers.find((candidate) => candidate.available)?.id ?? current);
        });
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [notebookId, setAgentEvents, workspaceReconnectSequence]);

  useEffect(() => {
    window.localStorage.setItem("renart-notebook-agent-provider", provider);
  }, [provider]);

  useEffect(() => {
    if (conversation?.status !== "running" && conversation?.status !== "cancelling") return;
    if (conversation.provider) setProvider(conversation.provider);
    if (conversation.mode) setMode(conversation.mode);
  }, [conversation?.mode, conversation?.provider, conversation?.status]);

  const turns = useMemo(
    () =>
      buildNotebookAgentTurns(
        conversation?.messages ?? [],
        conversation?.activities ?? [],
        conversation?.interaction,
      ),
    [conversation?.activities, conversation?.interaction, conversation?.messages],
  );
  const running = conversation?.status === "running" || conversation?.status === "cancelling";
  const selectedProvider = providers.find((candidate) => candidate.id === provider);
  const availableProviders = providers.filter((candidate) => candidate.available);
  const referenceCandidates = useMemo(
    () => buildNotebookAgentReferenceCandidates(notebookId, workspace),
    [notebookId, workspace],
  );

  const applyConversation = useCallback(
    (next: NonNullable<typeof conversation>) => {
      setAgentEvents((current) => mergeNotebookAgentEvent(current, next));
    },
    [setAgentEvents],
  );

  const send = async () => {
    const message = draft.trim();
    if (!message || running || requestBusy || !selectedProvider?.available) return;
    setDraft("");
    const submittedReferences = references;
    setReferences([]);
    setError("");
    setRequestBusy(true);
    try {
      applyConversation(
        await startNotebookAgentTurn(notebookId, {
          provider,
          mode,
          message,
          references: submittedReferences.map(({ kind, id }) => ({ kind, id })),
        }),
      );
    } catch (cause) {
      setDraft(message);
      setReferences(submittedReferences);
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setRequestBusy(false);
    }
  };

  const stop = async () => {
    if (!running || requestBusy) return;
    setError("");
    setRequestBusy(true);
    try {
      applyConversation(await cancelNotebookAgentTurn(notebookId));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setRequestBusy(false);
    }
  };

  const reset = async () => {
    if (running || requestBusy) return;
    setError("");
    setRequestBusy(true);
    try {
      applyConversation(await resetNotebookAgent(notebookId));
      setDraft("");
      setReferences([]);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setRequestBusy(false);
    }
  };

  const answerInteraction = async (
    interaction: NotebookAgentInteraction,
    input: Parameters<typeof answerNotebookAgentInteraction>[2],
  ) => {
    if (interactionBusy || interaction.status !== "pending") return;
    setError("");
    setInteractionBusy(true);
    try {
      applyConversation(await answerNotebookAgentInteraction(notebookId, interaction.id, input));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setInteractionBusy(false);
    }
  };

  return (
    <div className="flex size-full min-h-0 min-w-0 flex-col bg-background/80 backdrop-blur-sm">
      <div className="flex min-w-0 items-center gap-2 border-b px-3 py-2.5">
        <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
          <Bot aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium">Notebook assistant</div>
          <div className="truncate text-[10px] text-muted-foreground">
            Local agent · notebook tools only
          </div>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          title="New chat"
          disabled={running || requestBusy || turns.length === 0}
          onClick={() => void reset()}
        >
          <RotateCcw data-icon="inline-start" />
          <span className="sr-only">New chat</span>
        </Button>
        {onClose ? (
          <Button type="button" variant="ghost" size="icon-sm" title="Close" onClick={onClose}>
            <X data-icon="inline-start" />
            <span className="sr-only">Close notebook assistant</span>
          </Button>
        ) : null}
      </div>

      <div className="flex min-w-0 flex-wrap items-center gap-2 border-b px-3 py-2">
        <Select
          value={provider}
          disabled={running || requestBusy}
          onValueChange={(value) => setProvider(value as NotebookAgentProvider["id"])}
        >
          <SelectTrigger size="sm" className="min-w-32">
            <SelectValue placeholder="Choose agent" />
          </SelectTrigger>
          <SelectContent align="start">
            {providers.map((candidate) => (
              <SelectItem key={candidate.id} value={candidate.id} disabled={!candidate.available}>
                <span>{candidate.label}</span>
                {!candidate.available ? (
                  <span className="text-[10px] text-muted-foreground">Not installed</span>
                ) : null}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <ToggleGroup
          type="single"
          value={mode}
          variant="outline"
          size="sm"
          spacing={0}
          aria-label="Agent capability"
          disabled={running || requestBusy}
          onValueChange={(value) => {
            if (value === "ask" || value === "edit") setMode(value);
          }}
        >
          <ToggleGroupItem value="ask">Ask</ToggleGroupItem>
          <ToggleGroupItem value="edit">Edit</ToggleGroupItem>
        </ToggleGroup>
        <span className="min-w-0 flex-1 text-right text-[10px] text-muted-foreground">
          {mode === "ask" ? "Read-only" : "Can edit & run"}
        </span>
      </div>

      <div className="min-h-0 flex-1">
        {loading ? (
          <div className="flex size-full items-center justify-center gap-2 text-xs text-muted-foreground">
            <Spinner aria-label="Loading notebook assistant" />
            Loading chat…
          </div>
        ) : availableProviders.length === 0 ? (
          <Empty className="h-full rounded-none border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <Bot />
              </EmptyMedia>
              <EmptyTitle>Install a local coding agent</EmptyTitle>
              <EmptyDescription>
                Install and sign in to Codex, Claude Code, or OpenCode. Renart will discover it on
                PATH and connect it only to this notebook&apos;s semantic tools.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : turns.length === 0 ? (
          <Empty className="h-full rounded-none border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <Bot />
              </EmptyMedia>
              <EmptyTitle>Work on this notebook together</EmptyTitle>
              <EmptyDescription>
                Ask about cells, data, and diagnostics, or switch to Edit to let the agent apply and
                verify notebook changes while you watch.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <MessageScrollerProvider autoScroll defaultScrollPosition="last-anchor">
            <MessageScroller>
              <MessageScrollerViewport>
                <MessageScrollerContent className="gap-5 px-3 py-4">
                  {turns.map((turn) => (
                    <NotebookAgentTurnView
                      key={turn.user.turn_id}
                      turn={turn}
                      interactionBusy={interactionBusy}
                      onAnswerInteraction={answerInteraction}
                    />
                  ))}
                </MessageScrollerContent>
              </MessageScrollerViewport>
              <MessageScrollerButton />
            </MessageScroller>
          </MessageScrollerProvider>
        )}
      </div>

      <div className="border-t bg-background p-3">
        {error || conversation?.error ? (
          <Alert variant="destructive" className="mb-2">
            <CircleAlert />
            <AlertTitle>Could not continue the chat</AlertTitle>
            <AlertDescription>{error || conversation?.error}</AlertDescription>
          </Alert>
        ) : null}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void send();
          }}
        >
          <InputGroup className="bg-background">
            {references.length > 0 ? (
              <InputGroupAddon align="block-start" className="flex-wrap gap-1.5">
                {references.map((reference) => (
                  <InputGroupButton
                    key={`${reference.kind}:${reference.id}`}
                    type="button"
                    variant="outline"
                    size="xs"
                    title={`Remove ${reference.kind} reference ${reference.label}`}
                    onClick={() =>
                      setReferences((current) =>
                        current.filter(
                          (candidate) =>
                            candidate.kind !== reference.kind || candidate.id !== reference.id,
                        ),
                      )
                    }
                  >
                    <AtSign data-icon="inline-start" />
                    <span className="max-w-36 truncate">{reference.label}</span>
                    <X data-icon="inline-end" />
                  </InputGroupButton>
                ))}
              </InputGroupAddon>
            ) : null}
            <InputGroupTextarea
              value={draft}
              rows={2}
              maxLength={32 << 10}
              placeholder={
                mode === "ask"
                  ? "Ask about this notebook…"
                  : "Describe what to change in this notebook…"
              }
              disabled={
                !selectedProvider?.available || conversation?.interaction?.status === "pending"
              }
              className="max-h-44 min-h-16"
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (
                  event.key === "@" &&
                  !event.altKey &&
                  !event.ctrlKey &&
                  !event.metaKey &&
                  referenceCandidates.length > 0
                ) {
                  event.preventDefault();
                  setReferencePickerOpen(true);
                  return;
                }
                if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  void send();
                }
              }}
            />
            <InputGroupAddon align="block-end" className="justify-between">
              <div className="flex min-w-0 items-center gap-1">
                <NotebookAgentReferencePicker
                  open={referencePickerOpen}
                  onOpenChange={setReferencePickerOpen}
                  candidates={referenceCandidates}
                  selected={references}
                  disabled={running || requestBusy || referenceCandidates.length === 0}
                  onSelect={(reference) => {
                    setReferences((current) => {
                      if (current.length >= maxNotebookAgentReferences) return current;
                      const exists = current.some(
                        (candidate) =>
                          candidate.kind === reference.kind && candidate.id === reference.id,
                      );
                      return exists ? current : [...current, reference];
                    });
                    setReferencePickerOpen(false);
                  }}
                />
                <span className="truncate text-[10px] font-normal">
                  {selectedProvider?.label ?? "Local agent"} · {mode === "ask" ? "Ask" : "Edit"}
                </span>
              </div>
              {running ? (
                <InputGroupButton
                  type="button"
                  variant="outline"
                  size="sm"
                  className="ml-auto"
                  disabled={requestBusy || conversation?.status === "cancelling"}
                  onClick={() => void stop()}
                >
                  {conversation?.status === "cancelling" ? (
                    <Spinner aria-label="Stopping agent" />
                  ) : (
                    <Square data-icon="inline-start" className="fill-current" />
                  )}
                  Stop
                </InputGroupButton>
              ) : (
                <InputGroupButton
                  type="submit"
                  variant="default"
                  size="icon-xs"
                  className="ml-auto shrink-0 rounded-full"
                  aria-label="Send message"
                  title="Send message"
                  disabled={!draft.trim() || requestBusy || !selectedProvider?.available}
                >
                  {requestBusy ? <Spinner aria-label="Starting agent" /> : <ArrowUp />}
                  <span className="sr-only">Send message</span>
                </InputGroupButton>
              )}
            </InputGroupAddon>
          </InputGroup>
        </form>
      </div>
    </div>
  );
}

function NotebookAgentTurnView({
  turn,
  interactionBusy,
  onAnswerInteraction,
}: {
  turn: NotebookAgentTurn;
  interactionBusy: boolean;
  onAnswerInteraction: (
    interaction: NotebookAgentInteraction,
    input: Parameters<typeof answerNotebookAgentInteraction>[2],
  ) => void | Promise<void>;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <MessageScrollerItem messageId={turn.user.id} scrollAnchor>
        <AgentMessage message={turn.user} />
      </MessageScrollerItem>
      {turn.activities.length > 0 ? (
        <MessageScrollerItem messageId={`${turn.user.turn_id}:activities`}>
          <div className="ml-9 flex min-w-0 flex-col gap-1.5 rounded-lg border bg-muted/25 px-2.5 py-2">
            {turn.activities.map((activity) => (
              <AgentActivity key={activity.id} activity={activity} />
            ))}
          </div>
        </MessageScrollerItem>
      ) : null}
      {turn.interaction ? (
        <MessageScrollerItem messageId={turn.interaction.id}>
          <NotebookAgentInteractionView
            interaction={turn.interaction}
            busy={interactionBusy}
            onAnswer={(input) => onAnswerInteraction(turn.interaction!, input)}
          />
        </MessageScrollerItem>
      ) : null}
      {turn.assistant ? (
        <MessageScrollerItem messageId={turn.assistant.id}>
          <AgentMessage message={turn.assistant} />
        </MessageScrollerItem>
      ) : null}
    </div>
  );
}

function NotebookAgentInteractionView({
  interaction,
  busy,
  onAnswer,
}: {
  interaction: NotebookAgentInteraction;
  busy: boolean;
  onAnswer: (input: Parameters<typeof answerNotebookAgentInteraction>[2]) => void | Promise<void>;
}) {
  if (interaction.status !== "pending") {
    const answerSummary = summarizeInteractionAnswers(interaction);
    return (
      <div
        data-testid="notebook-agent-interaction-summary"
        className="ml-9 rounded-xl border bg-muted/25 px-3 py-2.5"
      >
        <div className="flex items-center gap-2 text-xs font-medium">
          {interaction.status === "answered" ? (
            <Check className="size-3.5 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <Square className="size-3.5 text-muted-foreground" />
          )}
          {interaction.status === "answered"
            ? interaction.kind === "connection_access"
              ? "Connection approved"
              : "You answered"
            : interaction.status === "declined"
              ? "You declined"
              : "Question cancelled"}
        </div>
        {answerSummary ? (
          <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">{answerSummary}</p>
        ) : null}
      </div>
    );
  }

  if (interaction.kind === "connection_access") {
    return (
      <NotebookAgentConnectionAccessView
        interaction={interaction}
        busy={busy}
        onAnswer={onAnswer}
      />
    );
  }

  const questions = interaction.questions ?? [];

  const items = questions.map((question) => ({
    name: question.id,
    required: question.required,
    choices: question.options?.map((option) => ({ value: option.value })),
  }));
  return (
    <div
      data-testid="notebook-agent-questionnaire"
      className="ml-9 rounded-xl border bg-card px-3 py-3 shadow-sm"
    >
      <div className="mb-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm font-semibold text-pretty">{interaction.title}</p>
            {interaction.description ? (
              <p className="mt-1 text-xs/relaxed text-pretty text-muted-foreground">
                {interaction.description}
              </p>
            ) : null}
          </div>
          <Badge variant="outline" className="shrink-0">
            Agent question
          </Badge>
        </div>
      </div>
      <Questionnaire
        defaultItem={questions[0]?.id}
        items={items}
        shortcuts="letters"
        aria-label={interaction.title}
        onSubmit={(event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          const answers: NotebookAgentQuestionAnswer[] = [];
          questions.forEach((question) => {
            if (question.kind === "text") {
              const text = String(data.get(question.id) ?? "").trim();
              if (text) {
                answers.push({ question_id: question.id, text });
              }
              return;
            }
            const values = data.getAll(question.id).map(String);
            if (values.length > 0) {
              answers.push({ question_id: question.id, values });
            }
          });
          void onAnswer({ answers });
        }}
      >
        {questions.length > 1 ? <QuestionnaireProgress /> : null}
        {questions.map((question) => (
          <QuestionnaireItem
            key={question.id}
            name={question.id}
            multiple={question.kind === "multiple_choice"}
            required={question.required}
            disabled={busy}
          >
            <QuestionnaireTitle>{question.prompt}</QuestionnaireTitle>
            {question.description ? (
              <QuestionnaireDescription>{question.description}</QuestionnaireDescription>
            ) : null}
            {question.kind === "text" ? (
              <QuestionnaireInput
                aria-label={question.prompt}
                maxLength={2 << 10}
                placeholder="Type your answer…"
              />
            ) : (
              <QuestionnaireChoices>
                {(question.options ?? []).map((option) => (
                  <QuestionnaireChoice key={option.value} value={option.value}>
                    <span className="flex min-w-0 items-center gap-2 font-medium">
                      <span className="truncate">{option.label}</span>
                      {option.recommended ? (
                        <Badge variant="secondary" className="shrink-0 text-[9px]">
                          Recommended
                        </Badge>
                      ) : null}
                    </span>
                    {option.description ? (
                      <QuestionnaireChoiceDescription>
                        {option.description}
                      </QuestionnaireChoiceDescription>
                    ) : null}
                  </QuestionnaireChoice>
                ))}
              </QuestionnaireChoices>
            )}
            <QuestionnaireError />
          </QuestionnaireItem>
        ))}
        <QuestionnaireActions>
          <QuestionnairePrevious size="sm" />
          <QuestionnaireSkip size="sm" />
          <QuestionnaireNext size="sm" />
          <QuestionnaireSubmit size="sm">
            {busy ? <Spinner aria-label="Sending answer" /> : null}
            Send answer
          </QuestionnaireSubmit>
        </QuestionnaireActions>
        <Button
          type="button"
          size="xs"
          variant="ghost"
          className="self-start text-muted-foreground"
          disabled={busy}
          onClick={() => void onAnswer({ declined: true })}
        >
          Decline question
        </Button>
      </Questionnaire>
    </div>
  );
}

function NotebookAgentConnectionAccessView({
  interaction,
  busy,
  onAnswer,
}: {
  interaction: NotebookAgentInteraction;
  busy: boolean;
  onAnswer: (input: Parameters<typeof answerNotebookAgentInteraction>[2]) => void | Promise<void>;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const settings = useWorkspaceSettingsData();
  const request = interaction.connection_request;
  const requestedName = request?.connection_name?.trim() ?? "";
  const requestedType = normalizeConnectionType(request?.connection_type);
  const [selectedConnection, setSelectedConnection] = useState("");
  const [connectionDialogOpen, setConnectionDialogOpen] = useState(false);

  const compatibleConnections = useMemo(() => {
    const configured = workspace?.query_connections ?? [];
    return configured.filter((connection) => {
      if (requestedName && connection.name.toLowerCase() !== requestedName.toLowerCase()) {
        return false;
      }
      if (request?.connection_type) {
        return normalizeConnectionType(connection.connection_type) === requestedType;
      }
      return true;
    });
  }, [request?.connection_type, requestedName, requestedType, workspace?.query_connections]);

  useEffect(() => {
    if (compatibleConnections.some((connection) => connection.name === selectedConnection)) return;
    setSelectedConnection(compatibleConnections[0]?.name ?? "");
  }, [compatibleConnections, selectedConnection]);

  const groups = useMemo<ConnectionSelectGroup[]>(
    () => [
      {
        label: "Configured in this environment",
        options: compatibleConnections.map((connection) => ({
          value: connection.name,
          label: connection.name,
          connectionType: connection.connection_type,
          detail: `${friendlyConnectionType(connection.connection_type)} · read-only for this turn`,
          badge: connection.name === requestedName ? "Requested" : undefined,
          badgeVariant: "secondary",
        })),
      },
    ],
    [compatibleConnections, requestedName],
  );
  const availableConnectionTypes = useMemo(
    () =>
      (settings.workspaceConfig?.connection_types ?? []).filter(
        (connectionType) =>
          connectionType.category === "warehouse" &&
          (!request?.connection_type ||
            normalizeConnectionType(connectionType.type_name) === requestedType),
      ),
    [request?.connection_type, requestedType, settings.workspaceConfig?.connection_types],
  );
  const environment =
    workspace?.selected_environment || settings.fallbackConfigEnvironment || "default";
  const capabilities = request?.capabilities?.length
    ? request.capabilities
    : (["discover", "sample_query"] as const);

  return (
    <div
      data-testid="notebook-agent-connection-access"
      className="ml-9 rounded-xl border bg-card px-3 py-3 shadow-sm"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold text-pretty">
            {request?.title || interaction.title || "Approve a data connection"}
          </p>
          <p className="mt-1 text-xs/relaxed text-pretty text-muted-foreground">
            {request?.description ||
              "The agent needs warehouse metadata or a small data sample to continue."}
          </p>
        </div>
        <Badge variant="outline" className="shrink-0">
          <ShieldCheck data-icon="inline-start" />
          Your approval
        </Badge>
      </div>

      <div className="mt-3 rounded-lg border bg-muted/20 p-2.5">
        <div className="flex items-start gap-2">
          <Plug className="mt-0.5 size-4 shrink-0 text-primary" />
          <div className="min-w-0 text-[11px]/relaxed text-muted-foreground">
            <p>
              Renart keeps credentials write-only. The agent receives only connection names, bounded
              catalog results, and up to 100 read-only sample rows. Approval expires when this Edit
              turn ends.
            </p>
            <div className="mt-2 flex flex-wrap gap-1">
              {capabilities.map((capability) => (
                <Badge key={capability} variant="secondary" size="xs">
                  {capability === "sample_query" ? "Sample queries" : "Browse catalog"}
                </Badge>
              ))}
            </div>
          </div>
        </div>
      </div>

      <div className="mt-3 grid gap-1.5">
        <label className="text-[11px] font-medium" htmlFor={`agent-connection-${interaction.id}`}>
          Connection
        </label>
        {compatibleConnections.length > 0 ? (
          <ConnectionSelect
            id={`agent-connection-${interaction.id}`}
            value={selectedConnection}
            groups={groups}
            contentAlign="start"
            className="w-full"
            disabled={busy}
            onValueChange={setSelectedConnection}
          />
        ) : (
          <div className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
            {requestedName ? (
              <>
                Connection <span className="font-mono text-foreground">{requestedName}</span> is not
                configured in {environment}.
              </>
            ) : request?.connection_type ? (
              <>No {friendlyConnectionType(request.connection_type)} connection is configured.</>
            ) : (
              <>No compatible query connection is configured.</>
            )}
          </div>
        )}
      </div>

      <div className="mt-3 flex flex-wrap items-center justify-end gap-2">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={busy}
          onClick={() => void onAnswer({ declined: true })}
        >
          Decline
        </Button>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={
            busy || settings.workspaceConfigLoading || availableConnectionTypes.length === 0
          }
          onClick={() => setConnectionDialogOpen(true)}
        >
          <Plug data-icon="inline-start" />
          New connection
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={busy || !selectedConnection}
          onClick={() => void onAnswer({ connection_name: selectedConnection })}
        >
          {busy ? <Spinner data-icon="inline-start" /> : <Check data-icon="inline-start" />}
          Approve for this turn
        </Button>
      </div>

      <WorkspaceConnectionDialog
        open={connectionDialogOpen}
        onOpenChange={setConnectionDialogOpen}
        environment={environment}
        connectionTypes={availableConnectionTypes}
        requestedConnectionType={request?.connection_type}
        requestedConnectionName={requestedName || undefined}
        onCreated={async (connectionName) => {
          await onAnswer({ connection_name: connectionName });
        }}
      />
    </div>
  );
}

function summarizeInteractionAnswers(interaction: NotebookAgentInteraction): string {
  if (interaction.kind === "connection_access") {
    return interaction.connection
      ? `${interaction.connection.name} · ${friendlyConnectionType(interaction.connection.connection_type)} · approved for this Edit turn`
      : "";
  }
  const questions = new Map(
    (interaction.questions ?? []).map((question) => [question.id, question]),
  );
  return (interaction.answers ?? [])
    .map((answer) => {
      const question = questions.get(answer.question_id);
      if (answer.text) return `${question?.prompt ?? answer.question_id}: ${answer.text}`;
      const labels = (answer.values ?? []).map(
        (value) => question?.options?.find((option) => option.value === value)?.label ?? value,
      );
      return labels.length > 0
        ? `${question?.prompt ?? answer.question_id}: ${labels.join(", ")}`
        : "";
    })
    .filter(Boolean)
    .join(" · ");
}

function AgentMessage({ message }: { message: NotebookAgentMessage }) {
  const user = message.role === "user";
  return (
    <Message align={user ? "end" : "start"}>
      <MessageAvatar
        className={cn(
          "size-7 min-w-7 border bg-background text-muted-foreground",
          user && "bg-primary/10 text-primary",
        )}
      >
        {user ? <UserRound className="size-3.5" /> : <Bot className="size-3.5" />}
      </MessageAvatar>
      <MessageContent>
        <MessageHeader>{user ? "You" : "Renart assistant"}</MessageHeader>
        {user && message.references?.length ? (
          <div className="flex max-w-full flex-wrap justify-end gap-1">
            {message.references.map((reference) => (
              <Badge
                key={`${reference.kind}:${reference.id}`}
                variant="outline"
                title={reference.detail}
                className="max-w-48"
              >
                <AtSign data-icon="inline-start" />
                <span className="truncate">{reference.label}</span>
              </Badge>
            ))}
          </div>
        ) : null}
        <Bubble variant={user ? "tinted" : "ghost"} align={user ? "end" : "start"}>
          <BubbleContent className={cn(user && "whitespace-pre-wrap", !user && "w-full")}>
            {user ? message.content : <AgentMarkdown content={message.content} />}
          </BubbleContent>
        </Bubble>
        {message.status === "streaming" ? (
          <MessageFooter>
            <span role="status" className="flex items-center gap-1">
              <Spinner aria-label="Agent is responding" />
              Responding…
            </span>
          </MessageFooter>
        ) : message.status === "cancelled" ? (
          <MessageFooter>Stopped</MessageFooter>
        ) : null}
      </MessageContent>
    </Message>
  );
}

function NotebookAgentReferencePicker({
  open,
  onOpenChange,
  candidates,
  selected,
  disabled,
  onSelect,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  candidates: NotebookAgentReferenceCandidate[];
  selected: NotebookAgentReferenceCandidate[];
  disabled: boolean;
  onSelect: (reference: NotebookAgentReferenceCandidate) => void;
}) {
  const selectedKeys = useMemo(
    () => new Set(selected.map((reference) => `${reference.kind}:${reference.id}`)),
    [selected],
  );
  const groups = [
    { id: "cells" as const, label: "Notebook cells" },
    { id: "assets" as const, label: "Workspace assets" },
  ];

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <InputGroupButton
          type="button"
          variant="ghost"
          size="sm"
          disabled={disabled}
          title="Reference a notebook cell or workspace asset"
        >
          <AtSign data-icon="inline-start" />
          Reference
        </InputGroupButton>
      </PopoverTrigger>
      <PopoverContent side="top" align="start" className="w-[min(22rem,calc(100vw-2rem))] p-0">
        <Command>
          <CommandInput placeholder="Search cells and assets…" />
          <CommandList>
            <CommandEmpty>No matching cells or assets.</CommandEmpty>
            {groups.map((group) => {
              const items = candidates.filter((candidate) => candidate.group === group.id);
              if (items.length === 0) return null;
              return (
                <CommandGroup key={group.id} heading={group.label}>
                  {items.map((candidate) => {
                    const selected = selectedKeys.has(`${candidate.kind}:${candidate.id}`);
                    const Icon = candidate.kind === "cell" ? FileCode : Database;
                    return (
                      <CommandItem
                        key={`${candidate.kind}:${candidate.id}`}
                        value={`${candidate.label} ${candidate.detail ?? ""}`}
                        disabled={
                          selected || (!selected && selectedKeys.size >= maxNotebookAgentReferences)
                        }
                        data-checked={selected}
                        onSelect={() => onSelect(candidate)}
                      >
                        <Icon />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate">{candidate.label}</span>
                          {candidate.detail ? (
                            <span className="block truncate text-[10px] text-muted-foreground">
                              {candidate.detail}
                            </span>
                          ) : null}
                        </span>
                      </CommandItem>
                    );
                  })}
                </CommandGroup>
              );
            })}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

function buildNotebookAgentReferenceCandidates(
  notebookId: string,
  workspace: WorkspaceState | null,
): NotebookAgentReferenceCandidate[] {
  const notebook = workspace?.notebooks?.find((candidate) => candidate.id === notebookId);
  const cells: NotebookAgentReferenceCandidate[] = (notebook?.cells ?? [])
    .filter((cell) => Boolean(cell.cell_id?.trim()))
    .map((cell) => ({
      kind: "cell",
      id: cell.cell_id!.trim(),
      label: cell.name.trim() || cell.cell_id!.trim(),
      detail: [cell.type, cell.connection].filter(Boolean).join(" · "),
      group: "cells",
    }));
  const assets: NotebookAgentReferenceCandidate[] = (workspace?.pipelines ?? []).flatMap(
    (pipeline) =>
      pipeline.assets.map((asset) => ({
        kind: "asset" as const,
        id: asset.id,
        label: asset.name,
        detail: [pipeline.name, asset.type, asset.connection].filter(Boolean).join(" · "),
        group: "assets" as const,
      })),
  );
  return [...cells, ...assets].sort((left, right) => {
    if (left.group !== right.group) return left.group === "cells" ? -1 : 1;
    return left.label.localeCompare(right.label);
  });
}

function AgentMarkdown({ content }: { content: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 text-xs/relaxed">
      <ReactMarkdown
        components={{
          p: ({ children }) => <p>{children}</p>,
          ul: ({ children }) => <ul className="list-disc pl-4">{children}</ul>,
          ol: ({ children }) => <ol className="list-decimal pl-4">{children}</ol>,
          li: ({ children }) => <li className="pl-0.5">{children}</li>,
          code: ({ children }) => (
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">{children}</code>
          ),
          pre: ({ children }) => (
            <pre className="max-w-full overflow-x-auto rounded-md bg-muted p-2 text-[11px]">
              {children}
            </pre>
          ),
          a: ({ children, href }) => (
            <a
              href={href}
              target="_blank"
              rel="noreferrer"
              className="text-primary underline underline-offset-2"
            >
              {children}
            </a>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}

function AgentActivity({ activity }: { activity: NotebookAgentActivity }) {
  return (
    <Marker
      role={activity.status === "running" ? "status" : undefined}
      className={cn(activity.status === "error" && "text-destructive")}
    >
      <MarkerIcon>
        {activity.status === "running" ? (
          <Spinner aria-label="In progress" />
        ) : activity.status === "complete" ? (
          <Check className="text-emerald-600 dark:text-emerald-400" />
        ) : activity.status === "error" ? (
          <CircleAlert />
        ) : (
          <Square />
        )}
      </MarkerIcon>
      <MarkerContent className="truncate" title={activity.detail || activity.title}>
        {activity.title}
      </MarkerContent>
    </Marker>
  );
}

function buildNotebookAgentTurns(
  messages: NotebookAgentMessage[],
  activities: NotebookAgentActivity[],
  interaction?: NotebookAgentInteraction,
): NotebookAgentTurn[] {
  const byTurn = new Map<string, NotebookAgentTurn>();
  const order: string[] = [];
  for (const message of messages) {
    let turn = byTurn.get(message.turn_id);
    if (!turn) {
      if (message.role !== "user") continue;
      turn = { user: message, activities: [] };
      byTurn.set(message.turn_id, turn);
      order.push(message.turn_id);
    }
    if (message.role === "assistant") turn.assistant = message;
  }
  for (const activity of activities) {
    byTurn.get(activity.turn_id)?.activities.push(activity);
  }
  if (interaction) {
    const turn = byTurn.get(interaction.turn_id);
    if (turn) turn.interaction = interaction;
  }
  return order
    .map((turnId) => byTurn.get(turnId))
    .filter((turn): turn is NotebookAgentTurn => !!turn);
}
