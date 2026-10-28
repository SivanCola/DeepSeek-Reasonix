import { useState } from "react";

const KEY = "rx-sidebar-folds";

function read(): Set<string> {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return new Set(Array.isArray(saved) ? saved.filter((key): key is string => typeof key === "string") : []);
  } catch {
    return new Set();
  }
}

export function useSidebarFolds(): [Set<string>, (next: Set<string>) => void] {
  const [folded, setFolded] = useState(read);
  const save = (next: Set<string>) => {
    setFolded(next);
    try {
      localStorage.setItem(KEY, JSON.stringify([...next]));
    } catch {
      // Folding remains available when this window cannot persist its choices.
    }
  };
  return [folded, save];
}

/** Section visibility is independent of each project's remembered fold. */
export function useProjectsFolded(): [boolean, (next: boolean) => void] {
  const key = "rx-projects-folded";
  const [folded, setFolded] = useState(() => {
    try { return localStorage.getItem(key) === "1"; } catch { return false; }
  });
  return [folded, (next) => {
    setFolded(next);
    try { localStorage.setItem(key, next ? "1" : "0"); } catch {
      // A private window still keeps the choice until the page closes.
    }
  }];
}
