export type WorkbenchOrganizeMode = "project" | "recent" | "time";
export type WorkbenchSortMode = "created" | "updated";

export const WORKBENCH_ORGANIZE_KEY = "projectTree:workbenchOrganize";
// Shared by classic and workbench; key string kept for existing saved choices.
export const WORKBENCH_SORT_KEY = "projectTree:workbenchSort";

export function loadWorkbenchOrganizeMode(): WorkbenchOrganizeMode {
  try {
    const value = localStorage.getItem(WORKBENCH_ORGANIZE_KEY);
    if (value === "recent" || value === "time") return value;
  } catch {
    /* localStorage unavailable */
  }
  return "project";
}

export function loadWorkbenchSortMode(): WorkbenchSortMode {
  try {
    const value = localStorage.getItem(WORKBENCH_SORT_KEY);
    if (value === "created") return "created";
  } catch {
    /* localStorage unavailable */
  }
  return "updated";
}
