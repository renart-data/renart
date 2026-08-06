"use client";

import { useEffect, useRef, useState } from "react";
import { Boxes, ListTree, LoaderCircle, TriangleAlert } from "lucide-react";

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { discoverLoadStreams, type LoadDiscoveryStream } from "@/lib/api-load";
import { cn } from "@/lib/utils";

type LoadStreamPickerProps = {
  id?: string;
  value: string;
  connection: string;
  environment?: string;
  placeholder?: string;
  ariaLabel?: string;
  mode?: "source" | "destination";
  variant?: "field" | "inline";
  onCommit: (value: string) => void;
};

// LoadStreamPicker keeps manual paths first-class while offering a
// credential-safe server-side listing. The browser receives object/table names
// only; configured connection secrets remain behind the Go endpoint.
export function LoadStreamPicker({
  id,
  value,
  connection,
  environment,
  placeholder = "schema.table or object path",
  ariaLabel,
  mode = "source",
  variant = "inline",
  onCommit,
}: LoadStreamPickerProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [streams, setStreams] = useState<LoadDiscoveryStream[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [truncated, setTruncated] = useState(false);
  const requestRef = useRef(0);

  useEffect(() => {
    if (!open || !connection) return;
    const token = ++requestRef.current;
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    setStreams([]);
    setTruncated(false);
    discoverLoadStreams({ connection, environment, signal: controller.signal })
      .then((result) => {
        if (token !== requestRef.current) return;
        if (result.status === "error") {
          setError(result.error || "Discovery failed.");
          setStreams([]);
          setTruncated(false);
        } else {
          setStreams(result.streams ?? []);
          setTruncated(Boolean(result.truncated));
        }
      })
      .catch((cause: unknown) => {
        if (token !== requestRef.current) return;
        if (cause instanceof DOMException && cause.name === "AbortError") return;
        setError(cause instanceof Error ? cause.message : "Discovery failed.");
        setStreams([]);
        setTruncated(false);
      })
      .finally(() => {
        if (token === requestRef.current) setLoading(false);
      });
    return () => controller.abort();
  }, [open, connection, environment]);

  const commit = (nextValue: string) => {
    const trimmed = nextValue.trim();
    if (!trimmed) return;
    onCommit(trimmed);
    setOpen(false);
  };

  const browseLabel = mode === "destination" ? "Browse destination objects" : "Browse sources";
  const filteredStreams = streams.filter((stream) => {
    const normalized = query.trim().toLowerCase();
    return !normalized || stream.name.toLowerCase().includes(normalized);
  });

  const picker = (
    <Popover
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (nextOpen) setQuery("");
      }}
    >
      <PopoverTrigger asChild>
        {variant === "field" ? (
          <InputGroupButton
            size="icon-xs"
            aria-label={browseLabel}
            title={connection ? browseLabel : "Choose a connection before browsing"}
            disabled={!connection}
          >
            <ListTree data-icon="inline-start" />
          </InputGroupButton>
        ) : (
          <button
            type="button"
            id={id}
            aria-label={ariaLabel}
            className={cn(
              "font-monaco min-w-0 flex-1 rounded-sm px-1 text-left outline-none hover:bg-muted/50 focus:bg-muted/60 focus:ring-1 focus:ring-ring",
              value ? "text-foreground" : "text-muted-foreground/60",
            )}
          >
            <span className="block truncate">{value || placeholder}</span>
          </button>
        )}
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-0">
        <Command shouldFilter={false}>
          <CommandInput
            placeholder={connection ? "Search or type a path…" : "Choose a connection first…"}
            value={query}
            onValueChange={setQuery}
            onKeyDown={(event) => {
              if (event.key === "Enter" && query.trim()) {
                event.preventDefault();
                commit(query);
              }
            }}
          />
          <CommandList>
            {loading ? (
              <div className="flex items-center gap-2 px-3 py-3 text-xs text-muted-foreground">
                <LoaderCircle className="animate-spin" />
                Discovering…
              </div>
            ) : null}
            {error ? <div className="px-3 py-3 text-xs text-destructive">{error}</div> : null}
            {!loading && !error && truncated ? (
              <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
                <TriangleAlert />
                Showing the first {streams.length} discovered entries.
              </div>
            ) : null}
            {!loading && !error && filteredStreams.length === 0 && !query.trim() ? (
              <CommandEmpty>
                {connection ? "No objects or tables found." : "Choose a connection to browse."}
              </CommandEmpty>
            ) : null}
            {query.trim() ? (
              <CommandGroup heading={mode === "destination" ? "New destination" : "Manual path"}>
                <CommandItem value={`__custom__${query}`} onSelect={() => commit(query)}>
                  <Boxes />
                  <span className="min-w-0 flex-1 truncate">Use “{query.trim()}”</span>
                </CommandItem>
              </CommandGroup>
            ) : null}
            {filteredStreams.length > 0 ? (
              <CommandGroup heading={mode === "destination" ? "Existing objects" : "Discovered"}>
                {filteredStreams.map((stream) => (
                  <CommandItem
                    key={stream.name}
                    value={stream.name}
                    data-checked={stream.name === value}
                    onSelect={() => commit(stream.name)}
                  >
                    <Boxes />
                    <span className="min-w-0 flex-1 truncate">{stream.name}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            ) : null}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );

  if (variant === "inline") {
    return picker;
  }

  return (
    <InputGroup>
      <InputGroupInput
        id={id}
        aria-label={ariaLabel}
        className="font-mono"
        placeholder={placeholder}
        value={value}
        onChange={(event) => onCommit(event.target.value)}
      />
      <InputGroupAddon align="inline-end">{picker}</InputGroupAddon>
    </InputGroup>
  );
}
