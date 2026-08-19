"use client";

import { Markdown } from "@tiptap/markdown";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { TableKit } from "@tiptap/extension-table";
import {
  Bold,
  Braces,
  Code2,
  Heading2,
  Italic,
  List,
  ListOrdered,
  Pilcrow,
  Quote,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { markdownContentClassName } from "@/lib/markdown-content";
import { cn } from "@/lib/utils";

export function MarkdownEditor({
  value,
  selected = false,
  placeholder = "Write something…",
  ariaLabel = "Markdown content",
  className,
  actions,
  onChange,
  onBlur,
}: {
  value: string;
  selected?: boolean;
  placeholder?: string;
  ariaLabel?: string;
  className?: string;
  actions?: React.ReactNode;
  onChange: (value: string) => void;
  onBlur?: () => void;
}) {
  const onChangeRef = useRef(onChange);
  const onBlurRef = useRef(onBlur);
  const syncingRef = useRef(false);
  const [sourceMode, setSourceMode] = useState(false);
  onChangeRef.current = onChange;
  onBlurRef.current = onBlur;

  const editor = useEditor({
    immediatelyRender: false,
    extensions: [
      StarterKit,
      TableKit,
      Markdown.configure({
        markedOptions: { gfm: true },
      }),
    ],
    content: value || "",
    contentType: "markdown",
    editorProps: {
      attributes: {
        "aria-label": ariaLabel,
        class: cn(
          "prose prose-sm min-h-20 max-w-none px-3 py-3 text-sm leading-6 text-foreground outline-none dark:prose-invert [&_h1]:mb-2 [&_h1]:text-xl [&_h1]:font-semibold [&_h2]:mb-2 [&_h2]:text-lg [&_h2]:font-semibold [&_p]:my-2 [&_table]:w-full [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:border-border [&_th]:bg-muted/40 [&_th]:px-2 [&_th]:py-1",
          markdownContentClassName,
        ),
      },
    },
    onUpdate: ({ editor: nextEditor }) => {
      if (!syncingRef.current) {
        onChangeRef.current(nextEditor.getMarkdown());
      }
    },
    onBlur: () => onBlurRef.current?.(),
  });

  useEffect(() => {
    if (!editor || editor.getMarkdown() === value) return;
    syncingRef.current = true;
    editor.commands.setContent(value || "", { contentType: "markdown", emitUpdate: false });
    syncingRef.current = false;
  }, [editor, value]);

  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      bold: current?.isActive("bold") ?? false,
      italic: current?.isActive("italic") ?? false,
      heading: current?.isActive("heading", { level: 2 }) ?? false,
      bulletList: current?.isActive("bulletList") ?? false,
      orderedList: current?.isActive("orderedList") ?? false,
      blockquote: current?.isActive("blockquote") ?? false,
      codeBlock: current?.isActive("codeBlock") ?? false,
    }),
  });

  return (
    <div
      className={cn(
        "group/markdown relative min-w-0 rounded-lg border border-transparent transition-colors hover:border-border focus-within:border-primary/35",
        selected && "border-primary/30 bg-background/55",
        className,
      )}
    >
      <div
        className={cn(
          "flex min-h-8 items-center gap-0.5 border-b border-transparent px-2 py-1 opacity-0 transition-opacity group-hover/markdown:opacity-100 group-focus-within/markdown:border-border group-focus-within/markdown:opacity-100",
          (selected || sourceMode) && "border-border opacity-100",
        )}
      >
        {!sourceMode && editor ? (
          <>
            <EditorCommandButton
              label="Bold"
              active={state?.bold}
              onClick={() => editor.chain().focus().toggleBold().run()}
            >
              <Bold />
            </EditorCommandButton>
            <EditorCommandButton
              label="Italic"
              active={state?.italic}
              onClick={() => editor.chain().focus().toggleItalic().run()}
            >
              <Italic />
            </EditorCommandButton>
            <EditorCommandButton
              label="Heading"
              active={state?.heading}
              onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
            >
              <Heading2 />
            </EditorCommandButton>
            <EditorCommandButton
              label="Bulleted list"
              active={state?.bulletList}
              onClick={() => editor.chain().focus().toggleBulletList().run()}
            >
              <List />
            </EditorCommandButton>
            <EditorCommandButton
              label="Numbered list"
              active={state?.orderedList}
              onClick={() => editor.chain().focus().toggleOrderedList().run()}
            >
              <ListOrdered />
            </EditorCommandButton>
            <EditorCommandButton
              label="Quote"
              active={state?.blockquote}
              onClick={() => editor.chain().focus().toggleBlockquote().run()}
            >
              <Quote />
            </EditorCommandButton>
            <EditorCommandButton
              label="Code block"
              active={state?.codeBlock}
              onClick={() => editor.chain().focus().toggleCodeBlock().run()}
            >
              <Code2 />
            </EditorCommandButton>
          </>
        ) : null}
        <span className="ml-auto" />
        <Button
          type="button"
          size="xs"
          variant="ghost"
          aria-label={sourceMode ? "Use visual Markdown editor" : "Edit Markdown source"}
          title={sourceMode ? "Visual editor" : "Markdown source"}
          onClick={() => setSourceMode((current) => !current)}
        >
          {sourceMode ? <Pilcrow /> : <Braces />}
          {sourceMode ? "Visual" : "Markdown"}
        </Button>
        {actions}
      </div>
      {sourceMode ? (
        <textarea
          aria-label="Markdown source"
          value={value}
          placeholder={placeholder}
          rows={Math.min(Math.max(value.split("\n").length, 5), 20)}
          className="min-h-28 w-full resize-y border-0 bg-transparent px-3 py-3 font-mono text-xs leading-5 outline-none"
          onChange={(event) => onChange(event.target.value)}
          onBlur={onBlur}
        />
      ) : (
        <div className="relative">
          {!value.trim() ? (
            <span className="pointer-events-none absolute top-3 left-3 text-sm text-muted-foreground">
              {placeholder}
            </span>
          ) : null}
          <EditorContent editor={editor} />
        </div>
      )}
    </div>
  );
}

function EditorCommandButton({
  label,
  active = false,
  children,
  onClick,
}: {
  label: string;
  active?: boolean;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      size="icon-xs"
      variant={active ? "secondary" : "ghost"}
      aria-label={label}
      title={label}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onClick}
    >
      {children}
    </Button>
  );
}
