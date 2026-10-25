// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import "./testkit";
import { Group, SettingSection } from "./Group";

afterEach(cleanup);

it("shares identical declared scope and timing while retaining each setting identity", () => {
  const { container } = render(<SettingSection title="显示" ids={["changed-files", "receipt"]}>
    <Group id="changed-files" title="文件改动" />
    <Group id="receipt" title="验收卡片" />
  </SettingSection>);
  expect(screen.getAllByText("本机 · 立即生效")).toHaveLength(1);
  for (const id of ["changed-files", "receipt"]) {
    const block = container.querySelector(`#set-${id}`)!;
    expect(block.getAttribute("aria-describedby")).toBeTruthy();
    expect(block.querySelector('[data-scope="machine"][data-apply="immediate"]')).toBeTruthy();
  }
});

it("keeps differing apply timing, unrelated scopes and save receipts explicit", () => {
  render(<SettingSection title="本机偏好" ids={["language", "persona"]}>
    <Group id="language" title="语言" />
    <Group id="persona" title="人设" saveState="dirty" />
    <Group id="plan-mode" title="计划模式" />
  </SettingSection>);
  expect(screen.getByText("本机")).toBeTruthy();
  expect(screen.getByText("重启后生效")).toBeTruthy();
  expect(screen.getByText("未保存 · 下次会话构建时生效")).toBeTruthy();
  expect(screen.getByText("当前会话 · 立即生效")).toBeTruthy();
  expect(screen.queryByText(/已保存/)).toBeNull();
});

it("does not suppress per-setting declarations when a group contains an unknown identity", () => {
  render(<SettingSection ids={["changed-files", "missing-setting"]}>
    <Group id="changed-files" title="文件改动" />
  </SettingSection>);
  expect(screen.getByText("本机 · 立即生效").getAttribute("data-scope")).toBe("machine");
});
