import assert from "node:assert/strict";
import { projectTreeApplyRuntimeTopics } from "../lib/projectTreeRuntime";
import type { ProjectNode } from "../lib/types";

type RuntimeNode = ProjectNode & { runtimeOnly?: boolean };
const catalog: ProjectNode[] = [
  { key: "project-a", kind: "project", label: "A", root: "/a", children: [
    { key: "known", kind: "topic", label: "Known", root: "/a", topicId: "known", children: [] },
  ] },
  { key: "project-b", kind: "project", label: "B", root: "/b", children: [] },
];
const overlaid = projectTreeApplyRuntimeTopics(catalog, [
  { scope: "project", workspaceRoot: "/a", node: {
    key: "known", kind: "topic", label: "Known live", root: "/a", topicId: "known", running: true, status: "thinking", children: [],
  } },
  { scope: "project", workspaceRoot: "/b", node: {
    key: "new", kind: "topic", label: "New live", root: "/b", topicId: "new", running: true, status: "streaming", children: [],
  } },
]);
const shape = (tree: ProjectNode[]) => tree.map((project) => project.children?.map((topic) => [
  topic.topicId, topic.running, (topic as RuntimeNode).runtimeOnly,
]));
assert.deepEqual(shape(overlaid), [[['known', true, undefined]], [['new', true, true]]]);
assert.deepEqual(shape(projectTreeApplyRuntimeTopics(overlaid, [])), [[['known', undefined, undefined]], []]);

const projects: ProjectNode[] = Array.from({ length: 100 }, (_, index) => ({
  key: `p-${index}`, kind: "project", label: `P ${index}`, root: `/p/${index}`, children: [],
}));
const topics = projects.map((project, index) => ({
  scope: "project", workspaceRoot: project.root, node: {
    key: `t-${index}`, kind: "topic" as const, label: `T ${index}`, root: project.root,
    topicId: `t-${index}`, running: true, status: "thinking" as const, children: [],
  },
}));
const hundred = projectTreeApplyRuntimeTopics(projects, topics);
assert.equal(hundred.reduce((count, project) => count + (project.children?.filter((topic) => topic.running).length ?? 0), 0), 100);
console.log("  PASS  project tree runtime projection");
