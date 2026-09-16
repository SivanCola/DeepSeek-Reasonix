import type { ProjectNode } from "./types";
import { sessionTitleTarget } from "./sessionTitleOperation";

export interface SessionTitleBindings {
  AIRenameSession(topicID: string): Promise<string>;
}

export function mockAIRenameSession(topic?: ProjectNode | null): string {
  if (!topic) return "";
  const title = topic.preview?.trim() || topic.label?.replace(/^●\s*/, "").trim() || "";
  if (title) topic.label = `${topic.label?.startsWith("● ") ? "● " : ""}${title}`;
  return title;
}

export function mockAIRenameTarget(nodes: ProjectNode[], target: string): string {
  const find = (rows: ProjectNode[]): ProjectNode | undefined => {
    for (const node of rows) {
      if (sessionTitleTarget(node) === target || node.topicId === target) return node;
      const child = find(node.children ?? []);
      if (child) return child;
    }
    return undefined;
  };
  return mockAIRenameSession(find(nodes));
}
