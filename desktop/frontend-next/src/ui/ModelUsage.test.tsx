// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import type { ModelEntry, RoleAssignments } from "../port/port";
import { ModelUsage } from "./ModelUsage";
import { writeProviderOrder } from "../state/providerorder";
import { accountKey } from "./vendors";

beforeEach(() => {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => void values.set(key, value),
  });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const MODELS: ModelEntry[] = [
  { ref: "deepseek/deepseek-flash", provider: "deepseek", vendor: "api.deepseek.com", model: "deepseek-flash", kind: "anthropic", vision: true, contextWindow: 1_000_000 },
  { ref: "deepseek/deepseek-pro", provider: "deepseek", vendor: "api.deepseek.com", model: "deepseek-pro", kind: "anthropic" },
];
const ROLES: RoleAssignments = { web_search: "", roles: { title: "", planner: "", subagent: "deepseek/deepseek-pro", vision: "", guardian: "", decision: "" }, efforts: {} };

function draw() {
  const onMain = vi.fn();
  const onRole = vi.fn();
  render(<ModelUsage models={MODELS} roles={ROLES} main="deepseek/deepseek-flash" busy="" protocol={{}} onMain={onMain} onRole={onRole} />);
  return { onMain, onRole };
}

const row = (name: string) => screen.getByText(name).closest("[role=row]") as HTMLElement;
const expandRoles = () => userEvent.click(screen.getByText("按用途指定模型"));

it("names each use, the model doing it, and the service it goes through", async () => {
  draw();
  expect((screen.getByRole("combobox", { name: "默认模型" }) as HTMLSelectElement).value).toBe("deepseek/deepseek-flash");
  expect(row("默认模型").textContent).toContain("读图 · 上下文 1M");
  await expandRoles();
  expect(row("计划").textContent).toContain("随主模型");
  expect((screen.getByRole("combobox", { name: "子代理" }) as HTMLSelectElement).value).toBe("deepseek/deepseek-pro");
});

it("says decision has no source instead of offering the chat models", async () => {
  draw();
  await expandRoles();
  const decision = screen.getByRole("combobox", { name: "决策" }) as HTMLSelectElement;
  expect(decision.disabled).toBe(true);
  expect(decision.textContent).toBe("尚无可用来源");
});

it("writes the row that changed", async () => {
  const { onMain, onRole } = draw();
  await expandRoles();
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "看图" }), "deepseek/deepseek-flash");
  expect(onRole).toHaveBeenCalledWith("vision", "deepseek/deepseek-flash", "auto");
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "默认模型" }), "deepseek/deepseek-pro");
  expect(onMain).toHaveBeenCalledWith("deepseek/deepseek-pro");
});

it("lists services in the saved order without reordering models within one service", () => {
  const other: ModelEntry = { ref: "other/chat", provider: "other", vendor: "other.example", model: "chat" };
  writeProviderOrder([accountKey("other.example"), accountKey("api.deepseek.com")]);
  render(<ModelUsage models={[...MODELS, other]} roles={ROLES} main={MODELS[0].ref} busy="" protocol={{}} onMain={() => {}} onRole={() => {}} />);
  const groups = screen.getByRole("combobox", { name: "默认模型" }).querySelectorAll("optgroup");
  expect([...groups].map((group) => group.label)).toEqual(["other", "deepseek"]);
  expect([...groups[1].querySelectorAll("option")].map((option) => option.value))
    .toEqual(["deepseek/deepseek-flash", "deepseek/deepseek-pro"]);
});

it("keeps naming effort fixed off even when the model exposes effort levels", async () => {
  const onRole = vi.fn();
  const onEffort = vi.fn();
  const models = MODELS.map((m) => ({ ...m, efforts: ["auto", "disabled", "high", "max"] }));
  render(<ModelUsage models={models} roles={{ ...ROLES, roles: { ...ROLES.roles, title: models[1].ref }, efforts: { title: "high", subagent: "high" } }}
    main={models[0].ref} effort="max" busy="" protocol={{}} onMain={() => {}} onRole={onRole} onEffort={onEffort} />);
  await expandRoles();
  expect(screen.queryByRole("combobox", { name: "自动命名的思考强度" })).toBeNull();
  const namingEffort = screen.getByLabelText("自动命名的思考强度");
  expect(namingEffort.textContent).toBe("不适用");
  expect(onRole).not.toHaveBeenCalled();
  expect(onEffort).not.toHaveBeenCalled();
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "子代理的思考强度" }), "max");
  expect(onRole).toHaveBeenCalledWith("subagent", models[1].ref, "max");
});

it("keeps the default model available and summarizes configured uses while the role table is closed", async () => {
  const onRole = vi.fn();
  render(<ModelUsage models={MODELS} roles={{ ...ROLES, efforts: { planner: "high" } }} main={MODELS[0].ref} busy="" protocol={{}}
    onMain={() => {}} onRole={onRole} />);
  expect(screen.getByRole("combobox", { name: "默认模型" })).toBeTruthy();
  expect(screen.getByRole("combobox", { name: "默认模型" }).closest("details")).toBeNull();
  expect(screen.getByRole("combobox", { name: "子代理" }).closest("details")?.open).toBe(false);
  const summary = screen.getByText("按用途指定模型").closest("summary")!;
  expect(summary.textContent).toContain("已配置 2 个用途：计划、子代理");
  await expandRoles();
  expect(within(screen.getByRole("table", { name: "按用途指定模型" })).getAllByRole("columnheader")).toHaveLength(4);
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "子代理" }), "");
  expect(onRole).toHaveBeenCalledWith("subagent", "", "auto");
});

it("keeps override conflicts discoverable and clears the same user override after opening", async () => {
  const onClearOverride = vi.fn();
  render(<ModelUsage models={MODELS} roles={ROLES} main={MODELS[0].ref} busy="" protocol={{}} onMain={() => {}} onRole={() => {}}
    overrides={{ subagent: [{ key: "task", model: MODELS[0].ref, scope: "user" }, { key: "review", model: MODELS[1].ref, scope: "project" }] }}
    onClearOverride={onClearOverride} />);
  expect(screen.getByText("按用途指定模型").closest("summary")?.textContent).toContain("2 项配置覆盖：子代理");
  await expandRoles();
  expect(screen.getByText("来自项目配置，需在项目里修改")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "改回跟随这里" }));
  expect(onClearOverride).toHaveBeenCalledWith("subagent", "task");
});

it("shows unsupported effort as text and preserves the missing-vision warning outside the closed roles", () => {
  const models = MODELS.map((model) => ({ ...model, vision: false }));
  render(<ModelUsage models={models} roles={ROLES} main={models[0].ref} busy="" protocol={{}} onMain={() => {}} onRole={() => {}} />);
  expect(screen.queryByRole("combobox", { name: "默认模型的思考强度" })).toBeNull();
  expect(screen.getByLabelText("默认模型的思考强度").textContent).toBe("不适用");
  expect(screen.getByText(/主模型无法识别的图片当前无人处理/).closest("details")).toBeNull();
});

it("opens role settings when the canonical model anchor is requested without resetting its selection", async () => {
  const props = { models: MODELS, roles: ROLES, main: MODELS[0].ref, busy: "", protocol: {}, onMain: vi.fn(), onRole: vi.fn() };
  const { rerender } = render(<ModelUsage {...props} />);
  const disclosure = screen.getByText("按用途指定模型").closest("details")!;
  expect(disclosure.open).toBe(false);
  rerender(<ModelUsage {...props} openRoles />);
  expect(disclosure.open).toBe(true);
  expect((screen.getByRole("combobox", { name: "子代理" }) as HTMLSelectElement).value).toBe(ROLES.roles.subagent);
  await expandRoles();
  expect(disclosure.open).toBe(false);
});

it("counts an explicit same-model assignment and preserves an unavailable assignment until cleared", async () => {
  const onRole = vi.fn();
  render(<ModelUsage models={MODELS} roles={{ ...ROLES, roles: { ...ROLES.roles, planner: MODELS[0].ref, subagent: "retired/model" } }}
    main={MODELS[0].ref} busy="" protocol={{}} onMain={() => {}} onRole={onRole} />);
  expect(screen.getByText("按用途指定模型").closest("summary")?.textContent).toContain("已配置 2 个用途：计划、子代理");
  await expandRoles();
  const subagent = screen.getByRole("combobox", { name: "子代理" }) as HTMLSelectElement;
  expect(subagent.value).toBe("retired/model");
  expect(subagent.textContent).toContain("retired/model（不可用）");
  await userEvent.selectOptions(subagent, "");
  expect(onRole).toHaveBeenCalledWith("subagent", "", "auto");
});
