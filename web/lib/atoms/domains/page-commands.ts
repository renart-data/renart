import { atom } from "jotai";

// Actions the current page offers in the command palette, such as Build's
// asset creation. The page registers them while it is mounted; the shortcut is
// a label only, the page handles its own keys.
export type PageCommand = {
  id: string;
  title: string;
  subtitle?: string;
  shortcut?: string;
  perform: () => void;
};

export const pageCommandsAtom = atom<PageCommand[]>([]);
