import type { Monaco } from "@monaco-editor/react";

let themesRegistered = false;

export function defineBruinMonacoThemes(monaco: Monaco) {
  if (themesRegistered) {
    return;
  }

  monaco.editor.defineTheme("bruin-vs", {
    base: "vs",
    inherit: true,
    semanticHighlighting: true,
    rules: [
      { token: "schema", foreground: "7c5a2a" },
      { token: "table", foreground: "0f766e" },
      { token: "column", foreground: "1d4ed8" },
      { token: "alias", foreground: "7c3aed" },
      { token: "schedule.preset", foreground: "7c3aed", fontStyle: "bold" },
      { token: "schedule.number", foreground: "1d4ed8" },
      { token: "schedule.wildcard", foreground: "0f766e", fontStyle: "bold" },
      { token: "schedule.operator", foreground: "7c5a2a" },
      { token: "schedule.invalid", foreground: "dc2626" },
    ],
    colors: {},
    semanticTokenColors: {
      schema: "#7c5a2a",
      table: "#0f766e",
      column: "#1d4ed8",
      alias: "#7c3aed",
    },
  });

  monaco.editor.defineTheme("bruin-vs-dark", {
    base: "vs-dark",
    inherit: true,
    semanticHighlighting: true,
    rules: [
      { token: "schema", foreground: "d6b36d" },
      { token: "table", foreground: "74cfc5" },
      { token: "column", foreground: "93c5fd" },
      { token: "alias", foreground: "c4b5fd" },
      { token: "schedule.preset", foreground: "c4b5fd", fontStyle: "bold" },
      { token: "schedule.number", foreground: "93c5fd" },
      { token: "schedule.wildcard", foreground: "74cfc5", fontStyle: "bold" },
      { token: "schedule.operator", foreground: "d6b36d" },
      { token: "schedule.invalid", foreground: "f87171" },
    ],
    colors: {},
    semanticTokenColors: {
      schema: "#d6b36d",
      table: "#74cfc5",
      column: "#93c5fd",
      alias: "#c4b5fd",
    },
  });

  // Notebook cells live inside the notebook document surface instead of an
  // opaque editor panel. Keep syntax colors intact while allowing the card's
  // transparent background to show through Monaco and its gutter.
  monaco.editor.defineTheme("bruin-notebook-vs", {
    base: "vs",
    inherit: true,
    semanticHighlighting: true,
    rules: [
      { token: "schema", foreground: "7c5a2a" },
      { token: "table", foreground: "0f766e" },
      { token: "column", foreground: "1d4ed8" },
      { token: "alias", foreground: "7c3aed" },
    ],
    colors: {
      "editor.background": "#00000000",
      "editorGutter.background": "#00000000",
      "editorStickyScroll.background": "#00000000",
      "editorOverviewRuler.background": "#00000000",
    },
    semanticTokenColors: {
      schema: "#7c5a2a",
      table: "#0f766e",
      column: "#1d4ed8",
      alias: "#7c3aed",
    },
  });

  monaco.editor.defineTheme("bruin-notebook-vs-dark", {
    base: "vs-dark",
    inherit: true,
    semanticHighlighting: true,
    rules: [
      { token: "schema", foreground: "d6b36d" },
      { token: "table", foreground: "74cfc5" },
      { token: "column", foreground: "93c5fd" },
      { token: "alias", foreground: "c4b5fd" },
    ],
    colors: {
      "editor.background": "#00000000",
      "editorGutter.background": "#00000000",
      "editorStickyScroll.background": "#00000000",
      "editorOverviewRuler.background": "#00000000",
    },
    semanticTokenColors: {
      schema: "#d6b36d",
      table: "#74cfc5",
      column: "#93c5fd",
      alias: "#c4b5fd",
    },
  });

  // Ad-hoc SQL is intentionally a scratch document rather than a repository
  // asset. Give it a restrained tint while retaining the same SQL semantic
  // colors as the regular editor themes.
  monaco.editor.defineTheme("bruin-adhoc-vs", {
    base: "vs",
    inherit: true,
    semanticHighlighting: true,
    rules: [
      { token: "schema", foreground: "7c5a2a" },
      { token: "table", foreground: "0f766e" },
      { token: "column", foreground: "1d4ed8" },
      { token: "alias", foreground: "7c3aed" },
    ],
    colors: {
      "editor.background": "#F6F7FC",
      "editorGutter.background": "#F1F3FA",
      "editorStickyScroll.background": "#F6F7FC",
    },
    semanticTokenColors: {
      schema: "#7c5a2a",
      table: "#0f766e",
      column: "#1d4ed8",
      alias: "#7c3aed",
    },
  });

  monaco.editor.defineTheme("bruin-adhoc-vs-dark", {
    base: "vs-dark",
    inherit: true,
    semanticHighlighting: true,
    rules: [
      { token: "schema", foreground: "d6b36d" },
      { token: "table", foreground: "74cfc5" },
      { token: "column", foreground: "93c5fd" },
      { token: "alias", foreground: "c4b5fd" },
    ],
    colors: {
      "editor.background": "#181A24",
      "editorGutter.background": "#151721",
      "editorStickyScroll.background": "#181A24",
    },
    semanticTokenColors: {
      schema: "#d6b36d",
      table: "#74cfc5",
      column: "#93c5fd",
      alias: "#c4b5fd",
    },
  });

  themesRegistered = true;
}
