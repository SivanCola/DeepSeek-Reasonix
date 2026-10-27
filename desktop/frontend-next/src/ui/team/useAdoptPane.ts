import { type Dispatch, type SetStateAction, useCallback } from "react";
import type { RuntimeView } from "../../port/hub";

// A pane the team opened in the kernel, brought into the window: listed once
// and focused, the way a pane the window opened itself would be.
export function useAdoptPane(setRuntimes: Dispatch<SetStateAction<RuntimeView[]>>, focusPane: (id: string) => void) {
  return useCallback(
    (rt: RuntimeView) => {
      setRuntimes((cur) => (cur.some((p) => p.id === rt.id) ? cur : [...cur, rt]));
      focusPane(rt.id);
    },
    [setRuntimes, focusPane],
  );
}
