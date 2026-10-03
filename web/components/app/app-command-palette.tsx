import { useNavigate } from "@tanstack/react-router";
import { useCommandState } from "cmdk";
import {
  BookOpen,
  Calendar,
  ChevronRight,
  FileCode,
  Hammer,
  Network,
  Play,
  Plus,
  Search,
  Settings2,
  Workflow,
} from "lucide-react";
import { ComponentType, useEffect, useMemo, useRef, useState } from "react";
import { useAtomValue } from "jotai";

import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from "@/components/ui/command";
import { Button } from "@/components/ui/button";
import { pageCommandsAtom } from "@/lib/atoms/domains/page-commands";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";

type PaletteItem = {
  id: string;
  title: string;
  subtitle?: string;
  keywords: string[];
  icon?: ComponentType<{ className?: string }>;
  perform: () => void;
};

type PalettePage = "pipelines" | "assets" | "notebooks";

type PaletteSection = {
  id: PalettePage;
  title: string;
  description: string;
  icon: ComponentType<{ className?: string }>;
  items: PaletteItem[];
};

function toKeywords(...values: Array<string | undefined | null>) {
  return values
    .flatMap((value) => (value ? value.split(/[^A-Za-z0-9_.-]+/) : []))
    .map((value) => value.trim())
    .filter(Boolean);
}

function PaletteSearchSubItem({ item }: { item: PaletteItem }) {
  const search = useCommandState((state) => state.search);

  if (!search.trim()) {
    return null;
  }

  return (
    <CommandItem
      value={`${item.title} ${item.subtitle ?? ""}`}
      keywords={item.keywords}
      onSelect={item.perform}
      className="ml-6"
    >
      <ChevronRight className="size-4 text-muted-foreground" />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="truncate">{item.title}</span>
        {item.subtitle ? (
          <span className="truncate text-xs text-muted-foreground">{item.subtitle}</span>
        ) : null}
      </div>
    </CommandItem>
  );
}

export function AppCommandPalette() {
  const navigate = useNavigate();
  const workspace = useAtomValue(workspaceAtom);
  const pageCommands = useAtomValue(pageCommandsAtom);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [pages, setPages] = useState<PalettePage[]>([]);
  const [shortcutLabel, setShortcutLabel] = useState("Ctrl K");
  const page = pages[pages.length - 1];
  // A page action can move focus (into a dialog or a canvas field). It runs
  // once the palette has closed, without the palette returning focus to the
  // element that opened it, so the action's own focus wins.
  const pendingCommandRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    const down = (event: KeyboardEvent) => {
      const target = event.target;
      const isEditable =
        target instanceof HTMLElement &&
        (target.isContentEditable || target.tagName === "INPUT" || target.tagName === "TEXTAREA");
      const isShortcut = event.key.toLowerCase() === "k" && (event.metaKey || event.ctrlKey);
      if (isEditable && !isShortcut) {
        return;
      }
      if (isShortcut) {
        event.preventDefault();
        setOpen((current) => !current);
      }
    };

    document.addEventListener("keydown", down);
    return () => document.removeEventListener("keydown", down);
  }, []);

  useEffect(() => {
    if (typeof navigator === "undefined") {
      return;
    }
    setShortcutLabel(navigator.platform.toLowerCase().includes("mac") ? "⌘K" : "Ctrl K");
  }, []);

  useEffect(() => {
    if (open) {
      return;
    }
    setSearch("");
    setPages([]);
  }, [open]);

  const close = () => setOpen(false);

  const pageItems = useMemo<PaletteItem[]>(() => {
    const navTo = (perform: () => void) => () => {
      perform();
      close();
    };
    return [
      {
        id: "page:build",
        title: "Build",
        subtitle: "Pipeline canvas & editor",
        icon: Hammer,
        to: () => navigate({ to: "/" }),
      },
      {
        id: "page:catalog",
        title: "Catalog",
        subtitle: "Asset lineage across the project",
        icon: Network,
        to: () => navigate({ to: "/catalog" }),
      },
      {
        id: "page:notebooks",
        title: "Notebooks",
        subtitle: "Exploratory DuckDB notebooks",
        icon: BookOpen,
        to: () => navigate({ to: "/notebooks" }),
      },
      {
        id: "page:runs",
        title: "Runs",
        subtitle: "Pipeline run history",
        icon: Play,
        to: () => navigate({ to: "/runs" }),
      },
      {
        id: "page:schedules",
        title: "Schedules",
        subtitle: "Scheduled pipeline runs",
        icon: Calendar,
        to: () => navigate({ to: "/schedules" }),
      },
      {
        id: "page:environments",
        title: "Environments",
        subtitle: "Project settings",
        icon: Settings2,
        to: () => navigate({ to: "/project/environments" }),
      },
      {
        id: "page:connections",
        title: "Connections",
        subtitle: "Project settings",
        icon: Settings2,
        to: () => navigate({ to: "/project/connections", search: {} }),
      },
    ].map(({ id, title, subtitle, icon, to }) => ({
      id,
      title,
      subtitle,
      icon,
      keywords: toKeywords(title, subtitle),
      perform: navTo(() => void to()),
    }));
  }, [navigate]);

  const sections = useMemo<PaletteSection[]>(() => {
    const pipelineItems: PaletteItem[] = [];
    const assetItems: PaletteItem[] = [];
    const notebookItems: PaletteItem[] = [];

    for (const pipeline of workspace?.pipelines ?? []) {
      pipelineItems.push({
        id: `pipeline:${pipeline.id}`,
        title: pipeline.name,
        subtitle: pipeline.path,
        keywords: toKeywords(pipeline.name, pipeline.path, pipeline.id),
        perform: () => {
          void navigate({
            to: "/pipelines/$pipelineId/canvas",
            params: { pipelineId: pipeline.id },
          });
          close();
        },
      });

      for (const asset of pipeline.assets ?? []) {
        assetItems.push({
          id: `asset:${asset.id}`,
          title: asset.name,
          subtitle: pipeline.name,
          keywords: toKeywords(asset.name, asset.path, asset.type, pipeline.name, asset.connection),
          perform: () => {
            void navigate({
              to: "/pipelines/$pipelineId/assets/$assetId/canvas",
              params: { pipelineId: pipeline.id, assetId: asset.id },
            });
            close();
          },
        });
      }
    }

    for (const notebook of workspace?.notebooks ?? []) {
      notebookItems.push({
        id: `notebook:${notebook.id}`,
        title: notebook.title,
        subtitle: notebook.path,
        keywords: toKeywords(notebook.title, notebook.path, notebook.id),
        perform: () => {
          void navigate({ to: "/notebooks/$notebookId", params: { notebookId: notebook.id } });
          close();
        },
      });
    }

    return [
      {
        id: "pipelines",
        title: "Go to pipeline...",
        description: "Open a pipeline canvas",
        icon: Workflow,
        items: pipelineItems,
      },
      {
        id: "assets",
        title: "Go to asset...",
        description: "Open an asset in the build view",
        icon: FileCode,
        items: assetItems,
      },
      {
        id: "notebooks",
        title: "Go to notebook...",
        description: "Open an exploratory notebook",
        icon: BookOpen,
        items: notebookItems,
      },
    ];
  }, [navigate, workspace?.notebooks, workspace?.pipelines]);

  const currentSection = sections.find((section) => section.id === page) ?? null;
  const inputPlaceholder = currentSection
    ? `Search ${currentSection.title.replace("...", "").toLowerCase()}`
    : pageCommands.length > 0
      ? "Search actions, assets, pipelines, notebooks, pages..."
      : "Search assets, pipelines, notebooks, pages...";

  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label="Search"
        title={`Search (${shortcutLabel})`}
        className="size-8 text-zinc-400 hover:bg-zinc-800 hover:text-white"
        onClick={() => setOpen(true)}
      >
        <Search className="size-4" />
      </Button>

      <CommandDialog
        open={open}
        onOpenChange={setOpen}
        title="Search"
        description="Search assets, pipelines, notebooks, and pages."
        className="max-w-2xl"
        onCloseAutoFocus={(event) => {
          const perform = pendingCommandRef.current;
          pendingCommandRef.current = null;
          if (!perform) return;
          event.preventDefault();
          perform();
        }}
      >
        <Command
          loop
          onKeyDown={(event) => {
            if (
              pages.length > 0 &&
              (event.key === "Escape" || (event.key === "Backspace" && !search.trim()))
            ) {
              event.preventDefault();
              setPages((current) => current.slice(0, -1));
            }
          }}
        >
          <CommandInput value={search} onValueChange={setSearch} placeholder={inputPlaceholder} />
          <CommandList>
            <CommandEmpty>No results found.</CommandEmpty>
            {currentSection ? (
              <CommandGroup heading={currentSection.title.replace("...", "")}>
                {currentSection.items.map((item) => (
                  <CommandItem
                    key={item.id}
                    value={`${item.title} ${item.subtitle ?? ""}`}
                    keywords={item.keywords}
                    onSelect={item.perform}
                  >
                    <ChevronRight className="size-4 text-muted-foreground" />
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate">{item.title}</span>
                      {item.subtitle ? (
                        <span className="truncate text-xs text-muted-foreground">
                          {item.subtitle}
                        </span>
                      ) : null}
                    </div>
                  </CommandItem>
                ))}
              </CommandGroup>
            ) : (
              <>
                {pageCommands.length > 0 ? (
                  <>
                    <CommandGroup heading="Actions">
                      {pageCommands.map((command) => (
                        <CommandItem
                          key={command.id}
                          value={`${command.title} ${command.subtitle ?? ""}`}
                          keywords={toKeywords(command.title, command.subtitle)}
                          onSelect={() => {
                            pendingCommandRef.current = command.perform;
                            close();
                          }}
                        >
                          <Plus className="size-4 text-muted-foreground" />
                          <div className="flex min-w-0 flex-1 flex-col">
                            <span className="truncate">{command.title}</span>
                            {command.subtitle ? (
                              <span className="truncate text-xs text-muted-foreground">
                                {command.subtitle}
                              </span>
                            ) : null}
                          </div>
                          {command.shortcut ? (
                            <CommandShortcut>{command.shortcut}</CommandShortcut>
                          ) : null}
                        </CommandItem>
                      ))}
                    </CommandGroup>
                    <CommandSeparator />
                  </>
                ) : null}
                <CommandGroup heading="Pages">
                  {pageItems.map((item) => {
                    const Icon = item.icon ?? ChevronRight;
                    return (
                      <CommandItem
                        key={item.id}
                        value={`${item.title} ${item.subtitle ?? ""}`}
                        keywords={item.keywords}
                        onSelect={item.perform}
                      >
                        <Icon className="size-4 text-muted-foreground" />
                        <div className="flex min-w-0 flex-1 flex-col">
                          <span className="truncate">{item.title}</span>
                          {item.subtitle ? (
                            <span className="truncate text-xs text-muted-foreground">
                              {item.subtitle}
                            </span>
                          ) : null}
                        </div>
                      </CommandItem>
                    );
                  })}
                </CommandGroup>
                <CommandSeparator />
                <CommandGroup heading="Jump to">
                  {sections.map((section) => {
                    const Icon = section.icon;
                    return (
                      <div key={section.id}>
                        <CommandItem
                          value={`${section.title} ${section.description}`}
                          keywords={section.items.flatMap((item) => item.keywords)}
                          onSelect={() => {
                            setSearch("");
                            setPages((current) => [...current, section.id]);
                          }}
                        >
                          <Icon className="size-4 text-muted-foreground" />
                          <div className="flex min-w-0 flex-1 flex-col">
                            <span className="truncate">{section.title}</span>
                            <span className="truncate text-xs text-muted-foreground">
                              {section.description}
                            </span>
                          </div>
                          <CommandShortcut>{section.items.length}</CommandShortcut>
                        </CommandItem>
                        {section.items.map((item) => (
                          <PaletteSearchSubItem key={item.id} item={item} />
                        ))}
                      </div>
                    );
                  })}
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
      </CommandDialog>
    </>
  );
}
