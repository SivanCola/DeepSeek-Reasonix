import { useState } from "react";
import { app } from "./bridge";
import { useT } from "./i18n";
import { useToast } from "./toast";
import { sessionTitleErrorKey } from "./sessionTitleOperation";

export function useSessionTitleOperation(refresh: () => Promise<void>, onTopicsChanged?: () => Promise<void> | void) {
  const [renaming, setRenaming] = useState<Set<string>>(() => new Set());
  const t = useT();
  const { showToast } = useToast();
  const rename = async (target: string) => {
    setRenaming(current => new Set(current).add(target));
    try {
      const title = await app.AIRenameSession(target);
      await refresh();
      await onTopicsChanged?.();
      if (title) showToast(t("projectTree.aiRenameDone", { title }));
    } catch (error) {
      showToast(t(sessionTitleErrorKey(error)), "error");
    } finally {
      setRenaming(current => {
        const next = new Set(current);
        next.delete(target);
        return next;
      });
    }
  };
  return { renaming, rename };
}
