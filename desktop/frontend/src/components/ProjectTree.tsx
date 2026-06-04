// ProjectTree is the sidebar replacement for the flat recent-sessions list.
// It shows a tree of projects (each with expandable topics) plus a Global
// section. Clicking a topic opens its tab; "+" next to a project creates a
// new topic.
import { useCallback, useEffect, useMemo, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent } from "react";
import { Archive, ChevronRight, ChevronDown, Pencil, Plus, Folder, FolderGit2, FolderPlus, MessageSquare, Search, BriefcaseBusiness, ListTree, Trash2, Copy, FolderOpen, XCircle } from "lucide-react";
import { asArray } from "../lib/array";
import { app } from "../lib/bridge";
import type { ProjectNode } from "../lib/types";
import { getLocale, useT, type Translator } from "../lib/i18n";
import { ContextMenu, contextMenuPointFromEvent, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { Tooltip } from "./Tooltip";

interface ProjectTreeProps {
  activeScope?: string;
  activeWorkspaceRoot?: string;
  activeTopicId?: string;
  currentWorkspaceName?: string;
  onOpenTopic: (scope: string, workspaceRoot: string, topicId: string) => void;
  onAddProject: () => Promise<void>;
  onUseCurrentProject?: () => Promise<void>;
}

function projectNodeKey(node: ProjectNode, depth: number): string {
  return node.key || `${node.kind}-${node.root ?? ""}-${node.topicId ?? ""}-${depth}`;
}

function topicIsActive(node: ProjectNode, activeScope?: string, activeWorkspaceRoot?: string, activeTopicId?: string): boolean {
  if (node.kind !== "topic" && node.kind !== "global_topic") return false;
  const scope = node.kind === "global_topic" ? "global" : "project";
  return (
    activeTopicId === node.topicId &&
    activeScope === scope &&
    (scope === "global" || activeWorkspaceRoot === node.root)
  );
}

function topicMetaLine(node: ProjectNode, t: Translator): string {
  const turns = node.turns ?? 0;
  if (turns <= 0) return t("projectTree.newTopic");
  const last = node.lastActivityAt ? ` · ${topicActivityLabel(node.lastActivityAt)}` : "";
  return `${t(turns === 1 ? "history.turnOne" : "history.turnOther", { n: turns })}${last}`;
}

function topicActivityLabel(ms: number): string {
  if (ms <= 0) return "";
  const delta = Date.now() - ms;
  const locale = getLocale();
  const rtf = new Intl.RelativeTimeFormat(locale === "zh" ? "zh-CN" : "en", { numeric: "auto" });
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (delta < minute) return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (delta < hour) return rtf.format(-Math.max(1, Math.round(delta / minute)), "minute");
  if (delta < day) return rtf.format(-Math.round(delta / hour), "hour");
  if (delta < 7 * day) return rtf.format(-Math.round(delta / day), "day");
  return new Date(ms).toLocaleDateString();
}

export function ProjectTree({
  activeScope,
  activeWorkspaceRoot,
  activeTopicId,
  currentWorkspaceName,
  onOpenTopic,
  onAddProject,
  onUseCurrentProject,
}: ProjectTreeProps) {
  const t = useT();
  const [tree, setTree] = useState<ProjectNode[]>([]);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [manuallyCollapsed, setManuallyCollapsed] = useState<Set<string>>(new Set());
  const [newTitle, setNewTitle] = useState("");
  const [creatingUnder, setCreatingUnder] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [editingTopic, setEditingTopic] = useState<string | null>(null);
  const [topicDraft, setTopicDraft] = useState("");
  const [menuTopic, setMenuTopic] = useState<string | null>(null);
  const [menuProject, setMenuProject] = useState<{ key: string; root: string; path: string; scope: "global" | "project"; label: string } | null>(null);
  const [menuPoint, setMenuPoint] = useState<ContextMenuPoint | null>(null);
  const [editingProject, setEditingProject] = useState<{ key: string; root: string } | null>(null);
  const [projectDraft, setProjectDraft] = useState("");
  const [confirmAction, setConfirmAction] = useState<{ topicId: string; action: "delete" | "trash" } | null>(null);
  const [confirmRemoveProject, setConfirmRemoveProject] = useState<string | null>(null);

  const closeMenu = useCallback(() => {
    setMenuTopic(null);
    setMenuProject(null);
    setMenuPoint(null);
    setConfirmAction(null);
    setConfirmRemoveProject(null);
  }, []);

  const refresh = useCallback(async () => {
    try {
      const nodes = await app.ListProjectTree();
      const list = asArray(nodes);
      setTree(list);
      setExpanded((prev) => {
        const next = new Set(prev);
        for (const node of list) {
          if (node?.key && !manuallyCollapsed.has(node.key)) next.add(node.key);
        }
        return next;
      });
    } catch {
      /* bridge unavailable */
    }
  }, [manuallyCollapsed]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const toggleExpand = (key: string) => {
    const willCollapse = expanded.has(key);
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
    setManuallyCollapsed((prev) => {
      const next = new Set(prev);
      if (willCollapse) next.add(key);
      else next.delete(key);
      return next;
    });
  };

  const handleCreateTopic = async (scope: string, workspaceRoot: string) => {
    if (!newTitle.trim()) {
      setCreatingUnder(null);
      return;
    }
    try {
      await app.CreateTopic(scope, workspaceRoot, newTitle.trim());
      setNewTitle("");
      setCreatingUnder(null);
      await refresh();
    } catch {
      /* ignore */
    }
  };

  const startRenameTopic = (node: ProjectNode, label: string) => {
    setMenuTopic(null);
    setMenuProject(null);
    setMenuPoint(null);
    setConfirmAction(null);
    setEditingTopic(node.topicId ?? null);
    setTopicDraft(label);
  };

  const startRenameProject = (key: string, root: string, label: string) => {
    setMenuProject(null);
    setMenuTopic(null);
    setMenuPoint(null);
    setConfirmRemoveProject(null);
    setEditingProject({ key, root });
    setProjectDraft(label);
  };

  const commitRenameTopic = async (topicId: string) => {
    const title = topicDraft.trim();
    setEditingTopic(null);
    if (!title) return;
    try {
      await app.RenameTopic(topicId, title);
      await refresh();
    } catch {
      /* ignore */
    }
  };

  const commitRenameProject = async (root: string) => {
    const title = projectDraft.trim();
    setEditingProject(null);
    if (!title) return;
    try {
      await app.RenameProject(root, title);
      await refresh();
    } catch {
      /* ignore */
    }
  };

  const deleteTopic = async (topicId: string) => {
    try {
      await app.DeleteTopic(topicId);
      setMenuTopic(null);
      setMenuPoint(null);
      setConfirmAction(null);
      await refresh();
    } catch {
      /* ignore */
    }
  };

  const trashTopic = async (topicId: string) => {
    try {
      await app.TrashTopic(topicId);
      setMenuTopic(null);
      setMenuPoint(null);
      setConfirmAction(null);
      await refresh();
    } catch {
      /* ignore */
    }
  };

  const copyProjectPath = async (path: string) => {
    if (!path) return;
    try {
      await navigator.clipboard?.writeText(path);
    } catch {
      /* ignore */
    }
  };

  const removeProject = async (path: string) => {
    if (!path) return;
    try {
      await app.RemoveWorkspace(path);
      setMenuProject(null);
      setMenuPoint(null);
      setConfirmRemoveProject(null);
      await refresh();
    } catch {
      /* ignore */
    }
  };

  const visibleTree = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return tree;
    const matches = (node: ProjectNode) =>
      [node.label, node.root, node.topicId].some((value) => (value ?? "").toLowerCase().includes(q));
    const filterNode = (node: ProjectNode): ProjectNode | null => {
      const children = asArray(node.children)
        .map(filterNode)
        .filter((child): child is ProjectNode => child !== null);
      if (matches(node) || children.length > 0) return { ...node, children };
      return null;
    };
    return tree
      .map(filterNode)
      .filter((node): node is ProjectNode => node !== null);
  }, [query, tree]);

  const activeAncestorKeys = useMemo(() => {
    const walk = (nodes: ProjectNode[], ancestors: string[]): string[] | null => {
      for (const node of nodes) {
        if (!node) continue;
        if (topicIsActive(node, activeScope, activeWorkspaceRoot, activeTopicId)) return ancestors;
        const children = asArray(node.children);
        if (children.length > 0) {
          const next = walk(children, [...ancestors, projectNodeKey(node, ancestors.length)]);
          if (next) return next;
        }
      }
      return null;
    };
    return walk(tree, []) ?? [];
  }, [activeScope, activeTopicId, activeWorkspaceRoot, tree]);

  useEffect(() => {
    if (activeAncestorKeys.length === 0) return;
    setExpanded((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const key of activeAncestorKeys) {
        if (manuallyCollapsed.has(key) || next.has(key)) continue;
        next.add(key);
        changed = true;
      }
      return changed ? next : prev;
    });
  }, [activeAncestorKeys, manuallyCollapsed]);

  const renderNode = (node: ProjectNode | null | undefined, depth: number) => {
    if (!node) return null;
    const key = projectNodeKey(node, depth);
    const children = asArray(node.children);
    const isExpanded = query.trim() ? true : expanded.has(key);
    const hasChildren = children.length > 0;

    if (node.kind === "topic" || node.kind === "global_topic") {
      const scope = node.kind === "global_topic" ? "global" : "project";
      const active = topicIsActive(node, activeScope, activeWorkspaceRoot, activeTopicId);
      const label = (node.label || node.topicId || "Untitled").replace(/^●\s*/, "");
      const topicId = node.topicId ?? "";
      const topicMenuOpen = menuTopic === topicId;
      const openTopicMenu = (event: ReactMouseEvent<HTMLElement> | ReactKeyboardEvent<HTMLElement>) => {
        event.preventDefault();
        event.stopPropagation();
        setMenuProject(null);
        setConfirmRemoveProject(null);
        setMenuPoint(contextMenuPointFromEvent(event));
        setMenuTopic(topicId);
        setConfirmAction(null);
      };
      const topicMenuItems: ContextMenuItem[] = [
        {
          key: "rename",
          icon: <Pencil size={13} />,
          label: "重命名会话",
          onSelect: () => startRenameTopic(node, label),
        },
        {
          key: "delete",
          icon: <Trash2 size={13} />,
          label: confirmAction?.topicId === topicId && confirmAction.action === "delete" ? "确认删除会话" : "删除会话",
          danger: true,
          onSelect: () => {
            if (confirmAction?.topicId === topicId && confirmAction.action === "delete") void deleteTopic(topicId);
            else setConfirmAction({ topicId, action: "delete" });
          },
        },
        {
          key: "trash",
          icon: <Archive size={13} />,
          label: confirmAction?.topicId === topicId && confirmAction.action === "trash" ? "确认移入回收站" : "移到回收站",
          danger: true,
          onSelect: () => {
            if (confirmAction?.topicId === topicId && confirmAction.action === "trash") void trashTopic(topicId);
            else setConfirmAction({ topicId, action: "trash" });
          },
        },
      ];
      if (editingTopic === topicId) {
        return (
          <div
            key={key}
            className={`project-tree__topic project-tree__topic--editing${active ? " project-tree__topic--active" : ""}`}
            style={{ paddingLeft: 16 + depth * 16 }}
          >
            <input
              autoFocus
              className="project-tree__topic-input"
              value={topicDraft}
              onChange={(event) => setTopicDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void commitRenameTopic(topicId);
                if (event.key === "Escape") setEditingTopic(null);
              }}
              onBlur={() => void commitRenameTopic(topicId)}
            />
          </div>
        );
      }
      return (
        <div
          key={key}
          className={`project-tree__topic${active ? " project-tree__topic--active" : ""}${topicMenuOpen ? " project-tree__topic--menu-open" : ""}`}
          onContextMenu={openTopicMenu}
        >
          <button
            type="button"
            className="project-tree__topic-main"
            style={{ paddingLeft: 16 + depth * 16 }}
            onClick={() => onOpenTopic(scope, node.root ?? "", topicId)}
            onKeyDown={(event) => {
              if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
                openTopicMenu(event);
              }
            }}
          >
            <MessageSquare size={12} />
            {(node.open || node.running) && (
              <span
                className={`project-tree__topic-status${node.running ? " project-tree__topic-status--running" : ""}`}
                aria-hidden="true"
              />
            )}
            <span className="project-tree__topic-copy">
              <span className="project-tree__topic-label">{label}</span>
              <span className="project-tree__topic-meta">{topicMetaLine(node, t)}</span>
            </span>
          </button>
          {active && <ListTree className="project-tree__active-mark" size={16} />}
          <ContextMenu
            open={topicMenuOpen}
            point={menuPoint}
            items={topicMenuItems}
            minWidth={178}
            ariaLabel="会话操作"
            onClose={closeMenu}
          />
        </div>
      );
    }

    const scope = node.kind === "global_folder" ? "global" : "project";
    const projectRoot = scope === "global" ? "" : node.root ?? "";
    const projectPath = node.root ?? "";
    const projectLabel = node.label || (scope === "global" ? "Global" : "Untitled");
    const projectActive = activeScope === scope && (scope === "global" || activeWorkspaceRoot === node.root);
    const openProjectMenu = (event: ReactMouseEvent<HTMLElement> | ReactKeyboardEvent<HTMLElement>) => {
      event.preventDefault();
      event.stopPropagation();
      setMenuTopic(null);
      setConfirmAction(null);
      setMenuPoint(contextMenuPointFromEvent(event));
      setMenuProject({ key, root: projectRoot, path: projectPath, scope, label: projectLabel });
      setConfirmRemoveProject(null);
    };
    const projectMenuItems: ContextMenuItem[] = [
      {
        key: "new-session",
        icon: <Plus size={13} />,
        label: "新建会话",
        onSelect: () => {
          setMenuProject(null);
          setMenuPoint(null);
          setCreatingUnder(key);
          setNewTitle("");
        },
      },
      {
        key: "rename",
        icon: <Pencil size={13} />,
        label: "修改显示名称",
        onSelect: () => startRenameProject(key, projectRoot, projectLabel),
      },
      {
        key: "reveal",
        icon: <FolderOpen size={13} />,
        label: "在 Finder 中显示",
        disabled: !projectPath,
        onSelect: () => {
          void app.RevealPath(projectPath);
          closeMenu();
        },
      },
      {
        key: "copy-path",
        icon: <Copy size={13} />,
        label: "复制路径",
        disabled: !projectPath,
        onSelect: () => {
          void copyProjectPath(projectPath);
          closeMenu();
        },
      },
      ...(scope === "project"
        ? [
            { type: "separator" as const, key: "remove-separator" },
            {
              key: "remove",
              icon: <XCircle size={13} />,
              label: confirmRemoveProject === key ? "确认移出侧边栏" : "移出侧边栏",
              danger: true,
              onSelect: () => {
                if (confirmRemoveProject === key) void removeProject(projectPath);
                else setConfirmRemoveProject(key);
              },
            },
          ]
        : []),
    ];

    if (editingProject?.key === key) {
      return (
        <div key={key}>
          <div
            className={`project-tree__folder project-tree__folder--editing${projectActive ? " project-tree__folder--active" : ""}`}
            style={{ paddingLeft: 8 + depth * 16 }}
          >
            <input
              autoFocus
              className="project-tree__folder-input"
              value={projectDraft}
              onChange={(event) => setProjectDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void commitRenameProject(projectRoot);
                if (event.key === "Escape") setEditingProject(null);
              }}
              onBlur={() => void commitRenameProject(projectRoot)}
            />
          </div>
          {isExpanded && hasChildren && (
            <div className="project-tree__children">
              {children.map((child) => renderNode(child, depth + 1))}
            </div>
          )}
        </div>
      );
    }

    return (
      <div key={key}>
        <div
          className={`project-tree__folder${projectActive ? " project-tree__folder--active" : ""}${menuProject?.key === key ? " project-tree__folder--menu-open" : ""}`}
          onContextMenu={openProjectMenu}
        >
          <button
            type="button"
            className="project-tree__folder-main"
            style={{ paddingLeft: 8 + depth * 16 }}
            onClick={() => {
              if (hasChildren) toggleExpand(key);
            }}
            onKeyDown={(event) => {
              if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
                openProjectMenu(event);
              }
            }}
            aria-expanded={hasChildren ? isExpanded : undefined}
          >
            {hasChildren ? (
              isExpanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />
            ) : (
              <span style={{ width: 12 }} />
            )}
            <Folder size={12} />
            <span className="project-tree__folder-label">{projectLabel}</span>
          </button>
          <Tooltip label="新建会话">
            <button
              type="button"
              className="project-tree__new-topic"
              onClick={(e) => {
                e.stopPropagation();
                setCreatingUnder(key);
                setNewTitle("");
              }}
            >
              <Plus size={12} />
            </button>
          </Tooltip>
          <ContextMenu
            open={menuProject?.key === key}
            point={menuPoint}
            items={projectMenuItems}
            minWidth={212}
            ariaLabel="项目操作"
            onClose={closeMenu}
          />
        </div>
        {creatingUnder === key && (
          <div className="project-tree__new-input" style={{ paddingLeft: 28 + depth * 16 }}>
            <input
              autoFocus
              value={newTitle}
              onChange={(e) => setNewTitle(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  void handleCreateTopic(
                    node.kind === "global_folder" ? "global" : "project",
                    node.root ?? "",
                  );
                }
                if (e.key === "Escape") setCreatingUnder(null);
              }}
              onBlur={() => {
                if (!newTitle.trim()) setCreatingUnder(null);
                else void handleCreateTopic(
                  node.kind === "global_folder" ? "global" : "project",
                  node.root ?? "",
                );
              }}
              placeholder="会话名称"
            />
          </div>
        )}
        {isExpanded && hasChildren && (
          <div className="project-tree__children">
            {children.map((child) => renderNode(child, depth + 1))}
          </div>
        )}
      </div>
    );
  };

  return (
    <div className="project-tree">
      <label className="project-tree__search">
        <Search size={14} />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="搜索项目或会话"
        />
      </label>
      <div className="project-tree__header">
        <span className="project-tree__header-title">
          <BriefcaseBusiness size={13} />
          项目工作区
        </span>
      </div>
      <div className="project-tree__list">
        {visibleTree.length === 0 ? (
          query.trim() ? (
            <div className="project-tree__empty">没有匹配的项目或会话</div>
          ) : (
            <div className="project-tree__empty-state">
              <div className="project-tree__empty-icon">
                <FolderPlus size={18} />
              </div>
              <div className="project-tree__empty-copy">
                <strong>还没有项目</strong>
                <span>添加项目后，可以按项目管理会话、文件和上下文。</span>
              </div>
              <div className="project-tree__empty-actions">
                <button
                  type="button"
                  className="project-tree__empty-primary"
                  onClick={async () => {
                    await onAddProject();
                    await refresh();
                  }}
                >
                  <FolderPlus size={14} />
                  <span>添加项目</span>
                </button>
                {onUseCurrentProject && currentWorkspaceName && (
                  <button
                    type="button"
                    className="project-tree__empty-secondary"
                    onClick={async () => {
                      await onUseCurrentProject();
                      await refresh();
                    }}
                  >
                    <FolderGit2 size={14} />
                    <span>使用当前目录</span>
                  </button>
                )}
              </div>
              {currentWorkspaceName && <div className="project-tree__empty-current">当前目录：{currentWorkspaceName}</div>}
            </div>
          )
        ) : (
          visibleTree.map((node) => renderNode(node, 0))
        )}
      </div>
    </div>
  );
}
