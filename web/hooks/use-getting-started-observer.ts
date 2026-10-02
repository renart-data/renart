import { useAtomValue, useSetAtom } from "jotai";
import { useCallback } from "react";

import type { AssetStaleness } from "@/lib/api-staleness";
import {
  gettingStartedAtom,
  markGettingStartedAtom,
  observedEdit,
  observedRebuild,
} from "@/lib/getting-started";

// Edits and rebuilds show up in staleness, which arrives over SSE or with a
// page's own snapshot; both report here. Kept apart from the checklist UI so
// the pipeline canvas can observe without loading it.
export function useGettingStartedStalenessObserver() {
  const checklist = useAtomValue(gettingStartedAtom);
  const mark = useSetAtom(markGettingStartedAtom);
  const active = Boolean(checklist && !checklist.record.dismissed);
  const editedAt = checklist?.record.done.edit;
  return useCallback(
    (assets: AssetStaleness[]) => {
      if (!active) return;
      if (observedEdit(assets)) mark({ id: "edit" });
      if (observedRebuild(assets, editedAt)) mark({ id: "rebuild" });
    },
    [active, editedAt, mark],
  );
}
