"use client";

import { useAtomValue, useSetAtom } from "jotai";
import {
  AtSign,
  Bot,
  Check,
  CircleAlert,
  Database,
  FileCode,
  RotateCcw,
  Send,
  Square,
  UserRound,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";

import { Bubble, BubbleContent } from "@/components/ui/bubble";
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  cancelNotebookAgentTurn,
  getNotebookAgent,
  type NotebookAgentActivity,
  type NotebookAgentMessage,
  type NotebookAgentMode,
  type NotebookAgentProvider,
  type NotebookAgentReference,
  resetNotebookAgent,
  startNotebookAgentTurn,
} from "@/lib/api-notebooks";
import { mergeNotebookAgentEvent, notebookAgentEventsAtom } from "@/lib/atoms/domains/results";
import { workspaceAtom, workspaceReconnectSequenceAtom } from "@/lib/atoms/domains/workspace";
import type { WorkspaceState } from "@/lib/types";
import { cn } from "@/lib/utils";

type NotebookAgentTurn = {
  user: NotebookAgentMessage;
  assistant?: NotebookAgentMessage;
  activities: NotebookAgentActivity[];
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
    () => buildNotebookAgentTurns(conversation?.messages ?? [], conversation?.activities ?? []),
    [conversation?.activities, conversation?.messages],
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
                    <NotebookAgentTurnView key={turn.user.turn_id} turn={turn} />
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
              disabled={!selectedProvider?.available}
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
                  size="icon-sm"
                  title="Send message"
                  disabled={!draft.trim() || requestBusy || !selectedProvider?.available}
                >
                  {requestBusy ? (
                    <Spinner aria-label="Starting agent" />
                  ) : (
                    <Send data-icon="inline-start" />
                  )}
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

function NotebookAgentTurnView({ turn }: { turn: NotebookAgentTurn }) {
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
      {turn.assistant ? (
        <MessageScrollerItem messageId={turn.assistant.id}>
          <AgentMessage message={turn.assistant} />
        </MessageScrollerItem>
      ) : null}
    </div>
  );
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
  return order
    .map((turnId) => byTurn.get(turnId))
    .filter((turn): turn is NotebookAgentTurn => !!turn);
}
