// TabBar renders the browser-like workspace tab strip. Each tab represents one
// open project/global topic, so switching tabs switches the active conversation.
import { useEffect, useRef, useState } from "react";
import type { CSSProperties, DragEvent } from "react";
import { FileText, Plus, X } from "lucide-react";
import type { TabMeta } from "../lib/types";
import { projectColorValue } from "../lib/projectColors";
import { Tooltip } from "./Tooltip";

interface TabBarProps {
  tabs: TabMeta[];
  activeTabId?: string;
  onTabChange: (tabId: string) => void;
  onTabClose: (tabId: string) => void;
  onTabsReorder: (tabIds: string[]) => void;
  onNewTab: () => void;
  revealActiveSignal?: number;
}

type DropSide = "before" | "after";

function tabDisplayTitle(tab: TabMeta): string {
  if (tab.tabType === "file" || tab.scope === "file") return tab.topicTitle?.trim() || tab.filePath?.split("/").filter(Boolean).pop() || "File";
  if (tab.scope === "global") return "Global";
  const title = tab.topicTitle?.trim();
  return title || "Untitled";
}

function tabFullTitle(tab: TabMeta): string {
  if (tab.tabType === "file" || tab.scope === "file") return tab.filePath || tabDisplayTitle(tab);
  if (tab.scope === "global") return "Global";
  const workspaceName = tab.workspaceName?.trim() || "Project";
  return `${workspaceName} / ${tabDisplayTitle(tab)}`;
}

function projectAccentStyle(color?: string): CSSProperties | undefined {
  const value = projectColorValue(color);
  if (!value) return undefined;
  return { "--project-accent": value } as CSSProperties;
}

export function TabBar({ tabs, activeTabId, onTabChange, onTabClose, onTabsReorder, onNewTab, revealActiveSignal = 0 }: TabBarProps) {
  const [draggingTabId, setDraggingTabId] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<{ id: string; side: DropSide } | null>(null);
  const suppressClickRef = useRef(false);
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());
  const backendActiveTabId = tabs.find((tab) => tab.active)?.id;
  const activeTabIdExists = Boolean(activeTabId && tabs.some((tab) => tab.id === activeTabId));
  const resolvedActiveTabId = activeTabIdExists ? activeTabId : backendActiveTabId;
  const tabOrderKey = tabs.map((tab) => tab.id).join("\u0000");

  useEffect(() => {
    if (!resolvedActiveTabId) return;
    const frame = window.requestAnimationFrame(() => {
      tabRefs.current.get(resolvedActiveTabId)?.scrollIntoView({
        block: "nearest",
        inline: "nearest",
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [backendActiveTabId, resolvedActiveTabId, revealActiveSignal, tabOrderKey]);

  const handleClose = (tabId: string) => {
    onTabClose(tabId);
  };

  const clearDragState = () => {
    setDraggingTabId(null);
    setDropTarget(null);
  };

  const dropSideForEvent = (event: DragEvent<HTMLButtonElement>): DropSide => {
    const rect = event.currentTarget.getBoundingClientRect();
    return event.clientX > rect.left + rect.width / 2 ? "after" : "before";
  };

  const reorderTabIds = (draggedId: string, targetId: string, side: DropSide): string[] => {
    const ids = tabs.map((tab) => tab.id);
    const from = ids.indexOf(draggedId);
    const target = ids.indexOf(targetId);
    if (from < 0 || target < 0 || draggedId === targetId) return ids;
    const next = ids.filter((id) => id !== draggedId);
    const targetAfterRemoval = next.indexOf(targetId);
    const insertAt = side === "after" ? targetAfterRemoval + 1 : targetAfterRemoval;
    next.splice(insertAt, 0, draggedId);
    return next;
  };

  const handleDragStart = (event: DragEvent<HTMLButtonElement>, tabId: string) => {
    setDraggingTabId(tabId);
    setDropTarget(null);
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", tabId);
  };

  const handleDragOver = (event: DragEvent<HTMLButtonElement>, tabId: string) => {
    if (!draggingTabId || draggingTabId === tabId) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    setDropTarget({ id: tabId, side: dropSideForEvent(event) });
  };

  const handleDrop = (event: DragEvent<HTMLButtonElement>, tabId: string) => {
    event.preventDefault();
    const draggedId = draggingTabId || event.dataTransfer.getData("text/plain");
    const side = dropTarget?.id === tabId ? dropTarget.side : dropSideForEvent(event);
    clearDragState();
    if (!draggedId || draggedId === tabId) return;
    const next = reorderTabIds(draggedId, tabId, side);
    if (next.join("\u0000") !== tabs.map((tab) => tab.id).join("\u0000")) {
      suppressClickRef.current = true;
      onTabsReorder(next);
    }
  };

  const handleTabClick = (tabId: string) => {
    if (suppressClickRef.current) {
      suppressClickRef.current = false;
      return;
    }
    onTabChange(tabId);
  };

  return (
    <div className="tabbar">
      <div className="tabbar__tabs">
        {tabs.map((tab) => {
          const displayTitle = tabDisplayTitle(tab);
          const fullTitle = tabFullTitle(tab);
          return (
            <button
              key={tab.id}
              ref={(node) => {
                if (node) {
                  tabRefs.current.set(tab.id, node);
                } else {
                  tabRefs.current.delete(tab.id);
                }
              }}
              draggable
              className={[
                "tabbar__tab",
                tab.id === resolvedActiveTabId ? "tabbar__tab--active" : "",
                draggingTabId === tab.id ? "tabbar__tab--dragging" : "",
                dropTarget?.id === tab.id ? `tabbar__tab--drop-${dropTarget.side}` : "",
              ].filter(Boolean).join(" ")}
              title={fullTitle}
              aria-label={fullTitle}
              style={projectAccentStyle(tab.projectColor)}
              onClick={() => handleTabClick(tab.id)}
              onDragStart={(event) => handleDragStart(event, tab.id)}
              onDragOver={(event) => handleDragOver(event, tab.id)}
              onDrop={(event) => handleDrop(event, tab.id)}
              onDragEnd={clearDragState}
            >
              {tab.tabType === "file" || tab.scope === "file" ? (
                <FileText size={12} className="tabbar__file-icon" />
              ) : (
                <span className={`tabbar__status${tab.running ? " tabbar__status--running" : ""}`} />
              )}
              <span className="tabbar__tab-label">{displayTitle}</span>
              <span
                className="tabbar__tab-close"
                onClick={(e) => {
                  e.stopPropagation();
                  handleClose(tab.id);
                }}
              >
                <X size={10} />
              </span>
            </button>
          );
        })}
      </div>
      <Tooltip label="新建会话">
        <button className="tabbar__new" type="button" aria-label="新建会话" onClick={onNewTab}>
          <Plus size={13} />
        </button>
      </Tooltip>
    </div>
  );
}
