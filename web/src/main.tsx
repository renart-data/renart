import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { TooltipProvider } from "@/components/ui/tooltip";
import { warmMonacoEditorRuntime } from "@/lib/load-monaco-editor";
import { optOutOfUnsupportedViewTransitions } from "@/lib/view-transitions";
import { AppRouter } from "./router";
import "./globals.css";

optOutOfUnsupportedViewTransitions();
void warmMonacoEditorRuntime();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <TooltipProvider>
      <AppRouter />
    </TooltipProvider>
  </StrictMode>,
);
