// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import "./testkit";
import { Workspaces } from "./Workspaces";
import { ListingProgress } from "./ListingProgress";
import type { TreeWorkspace } from "../port/hub";

afterEach(cleanup);

const pending: TreeWorkspace = { root: "/project", name: "project", remembered: true, sessions: [], total: 0, countsComplete: false, listingState: "discovering" };

function tree(ws: TreeWorkspace, open = vi.fn(async () => {})) {
  return <Workspaces hub={{} as never} tree={[ws]} treeRead runtimes={[]} active="" folded={new Set()}
    onFold={() => {}} reload={async () => {}} onOpen={open} onFocus={() => {}} onClose={async () => {}}
    liveIds={() => []} runs={{}} onRename={() => {}} onError={() => {}}
    adder={{ add: () => {}, close: () => {}, at: null } as never} />;
}

it("keeps a discovered workspace visible until its history is known", () => {
  render(tree(pending));
  expect(screen.getByText("project")).toBeTruthy();
  expect(screen.getByText("正在读取历史摘要…")).toBeTruthy();
  expect(screen.queryByText("没有匹配的会话")).toBeNull();
  expect(screen.queryByText("尚无文件夹")).toBeNull();
});

it("opens pending history and replaces its placeholder with accurate metadata", () => {
  const open = vi.fn(async () => {});
  const row = { path: "/project/sessions/v4-a.jsonl", name: "v4-a", legacy: true, countsKnown: false, metadataState: "pending" as const };
  const { rerender } = render(tree({ ...pending, listingState: "indexing", sessions: [row] }, open));
  fireEvent.click(screen.getByText("v4-a"));
  expect(open).toHaveBeenCalledWith({ root: "/project", sessionPath: row.path });
  expect(screen.getByText("正在读取摘要")).toBeTruthy();
  rerender(tree({ ...pending, countsComplete: true, listingState: "ready", total: 1, sessions: [{ ...row, title: "Recovered title", turns: 2, metadataState: "ready", countsKnown: true }] }, open));
  expect(screen.getByText("Recovered title")).toBeTruthy();
  expect(screen.queryByText("正在读取摘要")).toBeNull();
  expect(screen.queryByText("正在读取历史摘要…")).toBeNull();
});

it("distinguishes incomplete search, failed metadata, and old servers", () => {
  const { rerender } = render(<ListingProgress workspace={pending} searching />);
  expect(screen.getByText("仍在搜索部分历史会话…")).toBeTruthy();
  rerender(<ListingProgress workspace={{ ...pending, listingState: "degraded" }} searching />);
  expect(screen.getByText("部分历史摘要暂不可用，可继续打开会话")).toBeTruthy();
  expect(screen.getByRole("status").getAttribute("aria-busy")).toBe("false");
  rerender(<ListingProgress workspace={{ root: "/old", name: "old", sessions: [] }} searching />);
  expect(screen.queryByRole("status")).toBeNull();
});
