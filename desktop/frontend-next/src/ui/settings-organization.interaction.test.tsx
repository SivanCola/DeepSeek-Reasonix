// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { Settings } from "./Settings";
import { MockPort } from "../port/mock";
import { MockHub } from "../port/mock_hub";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

function draw(at: string, port = new MockPort(), home = port) {
  return render(<Settings hub={new MockHub()} port={port} networkPort={home} status={null}
    theme="light" onTheme={() => {}} contrast="" onContrast={() => {}} weight="" onWeight={() => {}}
    look={{}} onLook={() => {}} reloadThemes={() => {}} onClose={() => {}} onChanged={() => {}}
    onError={() => {}} at={at} account={null} accountUnread="" reloadAccount={() => {}} />);
}

it.each([
  ["appearance:live-stream-budget", "通用", "live-stream-budget"],
  ["appearance:notify", "通用", "notify"],
  ["appearance:folding", "会话", "folding"],
  ["account:backup-file", "数据与备份", "backup-file"],
])("keeps the old %s deep link usable after moving its block", async (at, page, anchor) => {
  draw(at);
  expect(await screen.findByRole("heading", { name: page, level: 2 })).toBeTruthy();
  const block = document.querySelector(`[data-setting="${anchor}"]`)!;
  expect(block).toBeTruthy();
  for (let parent = block.closest("details"); parent; parent = parent.parentElement?.closest("details") ?? null)
    expect(parent.open).toBe(true);
});

it("finds both kinds of conversation folding together and opens their disclosure", async () => {
  draw("general");
  await userEvent.type(screen.getByRole("textbox", { name: "搜索设置" }), "最后一句");
  await userEvent.click(await screen.findByRole("option", { name: /折叠中间过程/ }));
  expect(screen.getByRole("heading", { name: "会话", level: 2 })).toBeTruthy();
  const whole = document.querySelector('[data-setting="fold-work"]')!;
  const parts = document.querySelector('[data-setting="folding"]')!;
  expect(whole.closest("details")?.open).toBe(true);
  expect(whole.closest("details")).toBe(parts.closest("details"));
});

it("reads file backups from the local machine after relocation and does not require sign-in", async () => {
  const active = new MockPort(), home = new MockPort();
  const local = vi.spyOn(home, "backupFileCatalog");
  const remote = vi.spyOn(active, "backupFileCatalog");
  draw("storage:backup-file", active, home);
  await screen.findByRole("heading", { name: "导出与导入配置" });
  expect(local).toHaveBeenCalled();
  expect(remote).not.toHaveBeenCalled();
});
