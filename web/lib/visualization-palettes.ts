import type { VisualizationPalette } from "@/lib/api-notebooks";

export const VISUALIZATION_PALETTE_OPTIONS: Array<{
  value: VisualizationPalette;
  label: string;
  colors: string[];
}> = [
  {
    value: "default",
    label: "Workspace",
    colors: [
      "var(--chart-1)",
      "var(--chart-2)",
      "var(--chart-3)",
      "var(--chart-4)",
      "var(--chart-5)",
    ],
  },
  {
    value: "ocean",
    label: "Ocean",
    colors: ["#0f6b9e", "#1789b8", "#20a4b8", "#38b6a3", "#75c88a"],
  },
  {
    value: "sunset",
    label: "Sunset",
    colors: ["#d1495b", "#e66f51", "#ed9b40", "#f2c14e", "#c65d7b"],
  },
  {
    value: "forest",
    label: "Forest",
    colors: ["#285943", "#3c7a57", "#5b9a63", "#89b66b", "#b7c96f"],
  },
  {
    value: "berry",
    label: "Berry",
    colors: ["#5b3f8c", "#7e57a5", "#a45c9d", "#c96b8e", "#df8a7b"],
  },
  {
    value: "monochrome",
    label: "Monochrome",
    colors: [
      "color-mix(in srgb, var(--foreground) 92%, var(--background))",
      "color-mix(in srgb, var(--foreground) 76%, var(--background))",
      "color-mix(in srgb, var(--foreground) 60%, var(--background))",
      "color-mix(in srgb, var(--foreground) 44%, var(--background))",
      "color-mix(in srgb, var(--foreground) 28%, var(--background))",
    ],
  },
];

export function visualizationPaletteColors(value?: string): string[] {
  return (
    VISUALIZATION_PALETTE_OPTIONS.find((palette) => palette.value === value) ??
    VISUALIZATION_PALETTE_OPTIONS[0]
  ).colors;
}
