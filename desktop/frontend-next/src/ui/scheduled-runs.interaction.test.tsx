// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { ScheduledRuns } from "./ScheduledRuns";
import { HttpError } from "../port/http_error";
import type { AgentPort, ScheduleBoard, SchedulePreview } from "../port/port";

afterEach(cleanup);

const board = (over: Partial<ScheduleBoard> = {}): ScheduleBoard => ({
  pausedAll: false,
  folders: [{ path: "/Users/you/src/app", name: "app" }, { path: "/Users/you/src/docs", name: "docs" }],
  policy: { minIntervalMinutes: 60, maxSchedules: 8, globalTokensDay: 1_200_000, expiryDays: 30 },
  schedules: [{ id: "sch_a1", label: "Morning briefing", status: "active", everySec: 86_400, workspace: "/Users/you/src/app", model: "deepseek/v4", prompt: "p",
    perRunTokens: 250_000, lifetimeTokens: 3_000_000, spentLifetimeTokens: 410_000, expiresAtUnix: 1_793_000_000 }],
  runs: [{ triggerId: "sch_a1/1", scheduleId: "sch_a1", slotUnix: 1_790_000_000, state: "succeeded", chargedTokens: 118_400 }],
  ...over,
});

const preview: SchedulePreview = { firstRunUnix: 1_790_100_000, workspace: "/Users/you/src/app", model: "deepseek/v4", prompt: "summarize", perRunTokens: 250_000, perRunSteps: 40,
  perRunWallSec: 900, perDayMax: 1_000_000, lifetimeTokens: 3_000_000, expiresAtUnix: 1_792_700_000, confirm: "tok-1" };

function draw(over: Partial<AgentPort> = {}, b: ScheduleBoard = board()) {
  const port = {
    schedules: vi.fn(async () => b), previewSchedule: vi.fn(async () => preview), createSchedule: vi.fn(async () => b.schedules[0]),
    scheduleAction: vi.fn(async () => b), setSchedulesPaused: vi.fn(async () => b),
    scheduleResult: vi.fn(async () => ({ triggerId: "sch_a1/1", report: "all quiet", pending: [], enforcement: "tool-filter", recordedUnix: 1_790_000_260 })),
    ...over,
  } as unknown as AgentPort;
  render(<ScheduledRuns port={port} />);
  return port;
}

describe("scheduled runs", () => {
  it("lists what is scheduled with its trigger, status, spend and end", async () => {
    draw();
    expect(await screen.findByText("Morning briefing")).toBeTruthy();
    expect(screen.getByText(/每天 · 运行中/)).toBeTruthy();
    expect(screen.getByText(/已用 410,000 \/ 3,000,000 令牌/)).toBeTruthy();
    expect(screen.getByText("/Users/you/src/app · deepseek/v4")).toBeTruthy();
  });

  it("pauses without asking, but asks twice before deleting", async () => {
    const port = draw();
    await userEvent.click(await screen.findByRole("button", { name: "暂停" }));
    expect(port.scheduleAction).toHaveBeenCalledExactlyOnceWith("sch_a1", "pause");
    await userEvent.click(screen.getByRole("button", { name: "删除" }));
    expect(port.scheduleAction).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole("button", { name: "确认删除" }));
    expect(port.scheduleAction).toHaveBeenLastCalledWith("sch_a1", "delete");
  });

  it("shows the preview and creates only with that preview's confirmation", async () => {
    const port = draw();
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    expect((screen.getByRole("button", { name: "预览" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.selectOptions(screen.getByLabelText("文件夹（它只能读这里）"), "/Users/you/src/app");
    await userEvent.type(screen.getByLabelText("要做什么"), "summarize");
    await userEvent.click(screen.getByRole("button", { name: "预览" }));
    expect(port.previewSchedule).toHaveBeenCalledWith(expect.objectContaining({ workspace: "/Users/you/src/app", prompt: "summarize", everySec: 86400 }));
    const facts = await screen.findByRole("group", { name: "确认创建" });
    expect(within(facts).getByText(/每天 1,000,000 令牌，一共 3,000,000 令牌/)).toBeTruthy();
    expect(port.createSchedule).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "确认创建" }));
    expect(port.createSchedule).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ workspace: "/Users/you/src/app" }), "tok-1");
  });

  it("offers only the trusted projects, and says how to get one when there are none", async () => {
    draw();
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    const folder = screen.getByLabelText("文件夹（它只能读这里）") as HTMLSelectElement;
    expect([...folder.options].map((o) => o.value)).toEqual(["", "/Users/you/src/app", "/Users/you/src/docs"]);
    expect((screen.getByRole("button", { name: "预览" }) as HTMLButtonElement).disabled).toBe(true);
    cleanup();
    draw({}, board({ folders: [] }));
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    expect(screen.getByText(/还没有受信任的项目/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "预览" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers the open project for trust where a schedule needs it, and lists it once the answer is recorded", async () => {
    const trusted = board({ folders: [{ path: "/Users/you/src/app", name: "app" }] });
    const open = board({ folders: [], current: { path: "/Volumes/ssd/ERP", name: "ERP", trust: "", trustable: true } });
    let b = open;
    const decide = vi.fn(async () => { b = trusted; });
    const port = draw({ schedules: vi.fn(async () => b), decideWorkspaceTrust: decide } as Partial<AgentPort>, open);
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    expect(screen.getByText(/ERP/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "信任此文件夹" }));
    expect(decide).toHaveBeenCalledWith("trusted");
    expect(await screen.findByLabelText("文件夹（它只能读这里）")).toBeTruthy();
    expect(port.schedules).toHaveBeenCalledTimes(2);
  });

  it("says a declined project was declined, and offers nothing for one that cannot be trusted", async () => {
    draw({}, board({ folders: [], current: { path: "/Volumes/ssd", name: "ssd", trust: "declined", trustable: false } }));
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    expect(screen.queryByRole("button", { name: "信任此文件夹" })).toBeNull();
    expect(screen.getByText(/还没有受信任的项目/)).toBeTruthy();
  });

  it("goes back from the preview without creating anything", async () => {
    const port = draw();
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    await userEvent.selectOptions(screen.getByLabelText("文件夹（它只能读这里）"), "/Users/you/src/app");
    await userEvent.type(screen.getByLabelText("要做什么"), "x");
    await userEvent.click(screen.getByRole("button", { name: "预览" }));
    await userEvent.click(await screen.findByRole("button", { name: "返回修改" }));
    expect(screen.getByLabelText("要做什么")).toBeTruthy();
    expect(port.createSchedule).not.toHaveBeenCalled();
  });

  it("shows the kernel's refusal by its code and goes read-only without the edit grant", async () => {
    draw({ createSchedule: vi.fn(async () => { throw new HttpError(403, "x", { code: "schedule.editing_disabled", error: "no" }); }) });
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    await userEvent.selectOptions(screen.getByLabelText("文件夹（它只能读这里）"), "/Users/you/src/app");
    await userEvent.type(screen.getByLabelText("要做什么"), "x");
    await userEvent.click(screen.getByRole("button", { name: "预览" }));
    await userEvent.click(await screen.findByRole("button", { name: "确认创建" }));
    expect((await screen.findByRole("alert")).textContent).toBeTruthy();
    await waitFor(() => expect(screen.getByText("这些设置现在是只读的：这个窗口没有被授予编辑权限。")).toBeTruthy());
    expect((screen.getByRole("button", { name: "确认创建" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows a report as labelled plain text, never as markup or a remote load", async () => {
    draw({ scheduleResult: vi.fn(async () => ({ triggerId: "sch_a1/1", report: "<img src=x onerror=alert(1)> **bold** [link](https://evil.example)", pending: [{ kind: "approval", summary: "<img src=y onerror=alert(2)> Wanted a shell command", detail: "**git** [log](https://evil.example) <script>x</script>" }], enforcement: "tool-filter", recordedUnix: 1_790_000_260 })) });
    await userEvent.click(await screen.findByRole("button", { name: "查看结果" }));
    const region = await screen.findByRole("region", { name: "运行结果" });
    expect(within(region).getAllByText("以下是模型写的内容，只作参考，不是 Reasonix 的说明。")).toHaveLength(2);
    const report = within(region).getByTestId("schedule-report");
    expect(report.textContent).toContain("<img src=x onerror=alert(1)> **bold** [link](https://evil.example)");
    expect(region.querySelector("img, a, script")).toBeNull();
    const pending = within(region).getByTestId("schedule-pending");
    expect(pending.textContent).toContain("<img src=y onerror=alert(2)> Wanted a shell command");
    expect(pending.textContent).toContain("**git** [log](https://evil.example) <script>x</script>");
    expect(pending.querySelector("img, a, script")).toBeNull();
    expect(within(region).getByText("这些都没有被执行，也没有被代答。")).toBeTruthy();
  });

  it("says why the first read failed and offers a retry instead of dropping the block", async () => {
    const schedules = vi.fn().mockRejectedValueOnce(new HttpError(403, "x", { code: "schedule.editing_disabled", error: "no" })).mockResolvedValue(board());
    draw({ schedules });
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("无法读取定时运行：");
    expect(screen.getByText("定时运行")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("Morning briefing")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps the last good board when a later refresh fails, and says so", async () => {
    const schedules = vi.fn().mockResolvedValueOnce(board()).mockRejectedValueOnce(new Error("down")).mockResolvedValue(board());
    draw({ schedules });
    await userEvent.click(await screen.findByRole("button", { name: "暂停" }));
    expect(await screen.findByText("Morning briefing")).toBeTruthy();
    expect((await screen.findByRole("alert")).textContent).toContain("无法刷新定时运行：");
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(screen.getByText("Morning briefing")).toBeTruthy();
  });

  it("offers to resume everything when all are paused, and pauses all otherwise", async () => {
    const port = draw({}, board({ pausedAll: true }));
    expect(await screen.findByText("全部定时运行已暂停")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "全部恢复" }));
    expect(port.setSchedulesPaused).toHaveBeenCalledExactlyOnceWith(false);
    cleanup();
    const other = draw();
    await userEvent.click(await screen.findByRole("button", { name: "全部暂停" }));
    expect(other.setSchedulesPaused).toHaveBeenCalledExactlyOnceWith(true);
  });

  it("disables intervals under the policy's minimum", async () => {
    draw({}, board({ policy: { minIntervalMinutes: 240, maxSchedules: 8, globalTokensDay: 1_200_000, expiryDays: 30 } }));
    await userEvent.click(await screen.findByRole("button", { name: "新建定时运行" }));
    const every = screen.getByLabelText("多久一次") as HTMLSelectElement;
    const off = [...every.options].filter((o) => o.disabled).map((o) => o.textContent);
    expect(off).toEqual(["每小时", "每 2 小时"]);
  });
});
