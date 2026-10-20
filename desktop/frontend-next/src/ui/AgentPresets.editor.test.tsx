// @vitest-environment jsdom
import "./testkit";
import { act, cleanup, render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { AgentPresets } from "./AgentPresets";
import { MockPort } from "../port/mock";

afterEach(cleanup);

it("edits, exports and deletes a custom version without changing its conversation binding", async () => {
  const user = userEvent.setup();
  const port = new MockPort();
  const base = (await port.agentPresets()).presets[0].definition;
  const first = await port.saveAgentPreset({ ...base, id: "user:review", name: "Review" }, "");
  await port.selectAgentComposition(first.definition.id);
  render(<AgentPresets port={port} />);
  await user.click(await screen.findByRole("button", { name: "编辑 Review" }));
  expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "存放范围" }).disabled).toBe(true);
  await user.type(screen.getByLabelText("人设与要求"), "Literal requirements");
  await user.click(screen.getByRole("button", { name: "保存预设" }));
  await user.click(await screen.findByRole("button", { name: "编辑 Review" }));
  await user.click(screen.getByText("更多操作"));
  await user.click(screen.getByRole("button", { name: "导出文件" }));
  const link = await screen.findByRole<HTMLAnchorElement>("link", { name: "下载预设 JSON" });
  expect(JSON.parse(decodeURIComponent(link.href.split(",")[1])).persona.prefix).toBe("Literal requirements");
  await user.click(screen.getByRole("button", { name: "删除预设" }));
  await user.click(screen.getByRole("button", { name: "取消删除" }));
  expect((await port.agentPresets()).presets.some(v => v.definition.id === first.definition.id)).toBe(true);
  await user.click(screen.getByRole("button", { name: "删除预设" }));
  await user.click(screen.getByRole("button", { name: "确认删除" }));
  await screen.findByRole("button", { name: "新建预设" });
  expect(screen.queryByRole("button", { name: "编辑 Review" })).toBeNull();
  expect((await port.agentComposition())?.revision).toBe(first.revision);
});

it("does not let a late import replace a newly opened draft", async () => {
  const user = userEvent.setup();
  const port = new MockPort();
  const definition = (await port.agentPresets()).presets[0].definition;
  let finish!: (value: { definition: typeof definition; diagnostics: [] }) => void;
  vi.spyOn(port, "importAgentPreset").mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  render(<AgentPresets port={port} />);
  const input = await screen.findByLabelText("导入预设文件（先预览，再保存）");
  const file = new File(["{}"], "preset.json", { type: "application/json" });
  Object.defineProperty(file, "text", { value: async () => "{}" });
  await user.upload(input, file);
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  await user.click(screen.getByRole("button", { name: "新建预设" }));
  await user.type(screen.getByLabelText("预设名称"), "保留我的草稿");
  await act(async () => finish({ definition, diagnostics: [] }));
  expect((screen.getByLabelText("预设名称") as HTMLInputElement).value).toBe("保留我的草稿");
});

it("starts with a catalog and opens creation in a separate editor", async () => {
  const user = userEvent.setup();
  render(<AgentPresets port={new MockPort()} />);
  await screen.findByRole("button", { name: "新建预设" });
  expect(screen.queryByRole("textbox", { name: "人设与要求" })).toBeNull();
  await user.click(screen.getByRole("button", { name: "新建预设" }));
  const editor = await screen.findByRole("form", { name: "新建预设" });
  expect(within(editor).getByRole("textbox", { name: "预设名称" })).toBe(document.activeElement);
  expect(within(editor).getByText("基于日常搭档创建")).toBeTruthy();
  expect(within(editor).getByRole<HTMLButtonElement>("button", { name: "保存预设" }).disabled).toBe(true);
  expect(within(editor).getByLabelText("预设 ID").closest("details")).toBeNull();
  expect(within(editor).getByRole("textbox", { name: "补充要求" })).toBeTruthy();
  expect(within(editor).queryByText("高级设置")).toBeNull();
});

it("saves a named preset, returns to its catalog and leaves the conversation unbound", async () => {
  const user = userEvent.setup();
  const port = new MockPort();
  render(<AgentPresets port={port} />);
  await user.click(await screen.findByRole("button", { name: "新建预设" }));
  await user.type(screen.getByRole("textbox", { name: "预设名称" }), "代码审查助手");
  await user.type(screen.getByRole("textbox", { name: "人设与要求" }), "保留证据，说明风险。");
  await user.click(screen.getByRole("button", { name: "保存预设" }));
  await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
  expect(screen.getByRole("button", { name: "编辑 代码审查助手" })).toBeTruthy();
  const saved = (await port.agentPresets()).presets.find(v => v.definition.name === "代码审查助手")!;
  expect(saved.definition.persona.prefix).toBe("保留证据，说明风险。");
  expect(saved.definition.id.startsWith("user:")).toBe(true);
  expect(await port.agentComposition()).toBeNull();
});

it("guards Escape and cancel, retains edits, and restores focus after confirmed discard", async () => {
  const user = userEvent.setup();
  render(<AgentPresets port={new MockPort()} />);
  const create = await screen.findByRole("button", { name: "新建预设" });
  await user.click(create);
  await user.type(screen.getByRole("textbox", { name: "预设名称" }), "未保存的预设");
  await user.keyboard("{Escape}");
  expect(await screen.findByRole("alertdialog", { name: "放弃未保存的更改？" })).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "继续编辑" }));
  expect((screen.getByRole("textbox", { name: "预设名称" }) as HTMLInputElement).value).toBe("未保存的预设");
  await user.click(screen.getByRole("button", { name: "取消" }));
  await user.click(screen.getByRole("button", { name: "放弃更改" }));
  await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "新建预设" }));
});

it("keeps a refused save editable with the same draft", async () => {
  const user = userEvent.setup();
  const port = new MockPort();
  vi.spyOn(port, "saveAgentPreset").mockRejectedValue(new Error("revision conflict"));
  render(<AgentPresets port={port} />);
  await user.click(await screen.findByRole("button", { name: "新建预设" }));
  await user.type(screen.getByRole("textbox", { name: "预设名称" }), "冲突草稿");
  await user.click(screen.getByRole("button", { name: "保存预设" }));
  expect((await screen.findByRole("alert")).textContent).toContain("revision conflict");
  expect(screen.getByRole("form", { name: "新建预设" })).toBeTruthy();
  expect((screen.getByRole("textbox", { name: "预设名称" }) as HTMLInputElement).value).toBe("冲突草稿");
});

it("names the missing module and why when an imported preset needs one this host lacks", async () => {
  const user = userEvent.setup();
  const port = new MockPort();
  const definition = (await port.agentPresets()).presets[0].definition;
  vi.spyOn(port, "importAgentPreset").mockResolvedValue({ definition, diagnostics: [
    { code: "agent_composition.unavailable", module: "tools:browser_act", detail: "Dependency is not available on this host." }] });
  render(<AgentPresets port={port} />);
  const file = new File(["{}"], "preset.json", { type: "application/json" });
  Object.defineProperty(file, "text", { value: async () => "{}" });
  await user.upload(await screen.findByLabelText("导入预设文件（先预览，再保存）"), file);
  const said = await screen.findByText(/tools:browser_act/);
  expect(said.textContent).toContain("Dependency is not available on this host.");
});
