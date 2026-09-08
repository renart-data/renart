import { Search, X } from "lucide-react";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";
import type { BrowserCompletion, BrowserPathSyntax } from "@/lib/data-browser-search";
import { completionShadow, searchPathSegments } from "@/lib/data-browser-search-presentation";

export function DataBrowserSearchInput({
  value,
  onChange,
  completions,
  placeholder,
  pathSyntax,
}: {
  value: string;
  onChange: (value: string) => void;
  completions: BrowserCompletion[];
  placeholder: string;
  pathSyntax?: BrowserPathSyntax;
}) {
  const input = useRef<HTMLInputElement>(null);
  const shadow = useRef<HTMLSpanElement>(null);
  const hintId = useId();
  const [focused, setFocused] = useState(false);
  const [atEnd, setAtEnd] = useState(true);
  const [composing, setComposing] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const [index, setIndex] = useState(0);
  const [scrollLeft, setScrollLeft] = useState(0);
  const completion =
    focused && atEnd && !composing && !dismissed
      ? completions[index % (completions.length || 1)]
      : undefined;
  const suffix = completionShadow(value, completion);
  const segments = searchPathSegments(value, pathSyntax);
  // Radix Sheets observe Escape in document capture phase. Consume only this
  // field's visible completion first; a second Escape still closes the Sheet.
  useEffect(() => {
    if (!completion) return;
    const dismiss = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.target !== input.current || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      setDismissed(true);
    };
    window.addEventListener("keydown", dismiss, true);
    return () => window.removeEventListener("keydown", dismiss, true);
  }, [completion]);
  // Reserve enough scrollable space for the ghost suffix at the caret. Native
  // input scrolling alone would clip it as soon as a qualified path gets long.
  useLayoutEffect(() => {
    const element = input.current;
    if (!element) return;
    const reserve = completion
      ? Math.min(shadow.current?.getBoundingClientRect().width ?? 0, element.clientWidth * 0.55)
      : 0;
    element.style.paddingRight = `${reserve + 8}px`;
    if (completion) element.scrollLeft = element.scrollWidth;
    setScrollLeft(element.scrollLeft);
  }, [value, suffix, completion?.value]);
  const change = (next: string) => {
    setDismissed(false);
    setIndex(0);
    onChange(next);
  };
  const accept = () => {
    if (!completion) return;
    change(completion.value);
    requestAnimationFrame(() => {
      input.current?.focus();
      input.current?.setSelectionRange(completion.value.length, completion.value.length);
    });
  };

  return (
    <InputGroup>
      <div className="relative min-w-0 flex-1">
        {segments.some((segment) => segment.completed) ? (
          <div
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 flex items-center overflow-hidden px-2 text-sm text-transparent md:text-xs/relaxed"
          >
            <span className="whitespace-pre" style={{ transform: `translateX(${-scrollLeft}px)` }}>
              {segments.map((segment, position) => (
                <span
                  key={position}
                  data-search-segment={segment.completed || undefined}
                  className={segment.completed ? "rounded-xs bg-primary/10" : undefined}
                >
                  {segment.text}
                </span>
              ))}
            </span>
          </div>
        ) : null}
        <InputGroupInput
          className="relative"
          ref={input}
          value={value}
          maxLength={4096}
          aria-label="Search data browser"
          aria-autocomplete="inline"
          aria-describedby={hintId}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          placeholder={placeholder}
          onChange={(event) => change(event.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          onSelect={(event) =>
            setAtEnd(
              event.currentTarget.selectionStart === value.length &&
                event.currentTarget.selectionEnd === value.length,
            )
          }
          onScroll={(event) => setScrollLeft(event.currentTarget.scrollLeft)}
          onCompositionStart={() => setComposing(true)}
          onCompositionEnd={() => setComposing(false)}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing || event.altKey || event.ctrlKey || event.metaKey)
              return;
            if (event.key === "Tab" && !event.shiftKey && completion) {
              event.preventDefault();
              accept();
            } else if (event.key === "Escape" && completion) {
              event.preventDefault();
              event.stopPropagation();
              setDismissed(true);
            } else if (
              (event.key === "ArrowDown" || event.key === "ArrowUp") &&
              completion &&
              completions.length > 1
            ) {
              event.preventDefault();
              setIndex(
                (current) =>
                  (current + (event.key === "ArrowDown" ? 1 : completions.length - 1)) %
                  completions.length,
              );
            }
          }}
        />
        {completion && suffix ? (
          <div
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 flex items-center overflow-hidden px-2 text-sm text-muted-foreground md:text-xs/relaxed"
          >
            <span className="whitespace-pre" style={{ transform: `translateX(${-scrollLeft}px)` }}>
              <span className="invisible">{value}</span>
              <span ref={shadow} data-testid="data-browser-shadow-suggestion">
                {suffix}
              </span>
            </span>
          </div>
        ) : null}
      </div>
      <InputGroupAddon>
        <Search />
      </InputGroupAddon>
      <InputGroupAddon align="inline-end">
        {completion ? (
          <InputGroupButton
            aria-label={`Complete to ${completion.value}`}
            title={`Complete to ${completion.value}`}
            onPointerDown={(event) => event.preventDefault()}
            onClick={accept}
          >
            Tab
          </InputGroupButton>
        ) : null}
        {value ? (
          <InputGroupButton
            size="icon-xs"
            aria-label="Clear data browser search"
            onClick={() => {
              change("");
              input.current?.focus();
            }}
          >
            <X />
          </InputGroupButton>
        ) : null}
      </InputGroupAddon>
      <span id={hintId} className="sr-only" aria-live="polite">
        {completion
          ? `Complete to ${completion.value}. Press Tab or use the completion button. Escape dismisses; Shift Tab leaves the field.`
          : "Search this level, or enter connection.namespace. Storage paths use slashes."}
      </span>
    </InputGroup>
  );
}
